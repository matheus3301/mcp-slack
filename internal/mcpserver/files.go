package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/matheus3301/mcp-slack/internal/receipts"
	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/slack-go/slack"
)

const MaxFileBytes = 1 << 20

type FileInput struct {
	ChannelID string `json:"channel_id"`
	TS        string `json:"ts" jsonschema:"timestamp of the message containing the file"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	FileID    string `json:"file_id"`
}
type FileOutput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Title    string `json:"title"`
	Mimetype string `json:"mimetype"`
	Size     int    `json:"size"`
}
type DownloadOutput struct {
	File          FileOutput `json:"file"`
	ContentBase64 string     `json:"content_base64"`
}
type UploadInput struct {
	ChannelID     string `json:"channel_id"`
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"base64 encoded bytes, at most 1 MiB decoded; exclusive with artifact_path"`
	ArtifactPath  string `json:"artifact_path,omitempty" jsonschema:"relative regular file under the operator artifact root; exclusive with content_base64"`
	ThreadTS      string `json:"thread_ts,omitempty"`
	OperationKey  string `json:"operation_key"`
}

func fileView(f *slack.File) FileOutput { return FileOutput{f.ID, f.Name, f.Title, f.Mimetype, f.Size} }
func (t *Tools) authorizedFile(ctx context.Context, in FileInput) (*slack.File, error) {
	if !validID(in.FileID, "F") {
		return nil, invalid()
	}
	m, e := t.messageResource(ctx, MessageInput{in.ChannelID, in.TS, in.ThreadTS}, false)
	if e != nil {
		return nil, e
	}
	found := false
	for _, f := range m.Files {
		if f.ID == in.FileID {
			found = true
		}
	}
	if !found {
		return nil, denied()
	}
	var f *slack.File
	e = slackclient.Call(ctx, func() error {
		var err error
		f, _, _, err = t.Slack.GetFileInfoContext(ctx, in.FileID, 1, 1)
		return err
	})
	if e != nil {
		return nil, e
	}
	if f == nil || f.ID != in.FileID || f.IsExternal || f.Size < 0 || f.Size > MaxFileBytes {
		return nil, denied()
	}
	return f, nil
}
func (t *Tools) fileInfo(ctx context.Context, in FileInput) (FileOutput, error) {
	f, e := t.authorizedFile(ctx, in)
	if e != nil {
		return FileOutput{}, e
	}
	return fileView(f), nil
}
func safeSlackFileURL(raw, prefix string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == "files.slack.com" && u.User == nil && u.Fragment == "" && strings.HasPrefix(u.Path, prefix) && !strings.Contains(u.Path, "/../")
}

// Slack's file-storage tenant can differ from auth.test's workspace. The
// authorization proof is the exact file ID on the verified channel message.
func safeDownloadURL(raw, fileID string) bool {
	if !safeSlackFileURL(raw, "/files-pri/") {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/files-pri/"), "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return false
	}
	tenant, id, ok := strings.Cut(parts[0], "-")
	return ok && validID(tenant, "T") && validID(id, "F") && id == fileID
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxFileBytes-b.Len() {
		return 0, errors.New("file too large")
	}
	return b.Buffer.Write(p)
}
func (t *Tools) download(ctx context.Context, in FileInput) (DownloadOutput, error) {
	f, e := t.authorizedFile(ctx, in)
	if e != nil {
		return DownloadOutput{}, e
	}
	raw := f.URLPrivateDownload
	if raw == "" {
		raw = f.URLPrivate
	}
	if !safeDownloadURL(raw, f.ID) {
		return DownloadOutput{}, denied()
	}
	var b limitedBuffer
	// Do not retry a partially streamed download into the same buffer.
	if e = t.Slack.GetFileContext(ctx, raw, &b); e != nil {
		return DownloadOutput{}, e
	}
	if b.Len() != f.Size {
		return DownloadOutput{}, upstream()
	}
	return DownloadOutput{fileView(f), base64.StdEncoding.EncodeToString(b.Bytes())}, nil
}
func (t *Tools) uploadBytes(in UploadInput) ([]byte, error) {
	if in.Filename == "" || in.Filename == "." || in.Filename == ".." || filepath.Base(in.Filename) != in.Filename || len(in.Filename) > 128 || strings.ContainsAny(in.Filename, "/\\\x00\r\n") {
		return nil, invalid()
	}
	if (in.ContentBase64 == "") == (in.ArtifactPath == "") {
		return nil, invalid()
	}
	if in.ContentBase64 != "" {
		if len(in.ContentBase64) > base64.StdEncoding.EncodedLen(MaxFileBytes) {
			return nil, invalid()
		}
		b, e := base64.StdEncoding.Strict().DecodeString(in.ContentBase64)
		if e != nil || len(b) == 0 || len(b) > MaxFileBytes {
			return nil, invalid()
		}
		return b, nil
	}
	if t.Policy.ArtifactRoot == "" || !filepath.IsLocal(in.ArtifactPath) || strings.ContainsAny(in.ArtifactPath, "\\\x00") {
		return nil, denied()
	}
	root, e := os.OpenRoot(t.Policy.ArtifactRoot)
	if e != nil {
		return nil, denied()
	}
	defer root.Close()
	// Root confines symlink resolution during open, preventing TOCTOU escapes.
	// NONBLOCK prevents a raced FIFO/device from hanging before f.Stat rejects it.
	f, e := openArtifact(root, in.ArtifactPath)
	if e != nil {
		return nil, denied()
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > MaxFileBytes {
		return nil, denied()
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if e != nil || len(b) > MaxFileBytes || len(b) == 0 {
		return nil, denied()
	}
	return b, nil
}
func (t *Tools) upload(ctx context.Context, in UploadInput) (OperationOutput, error) {
	if !operationKeyRE.MatchString(in.OperationKey) {
		return OperationOutput{}, invalid()
	}
	_, a, e := t.authorize(ctx, in.ChannelID, true)
	if e != nil {
		return OperationOutput{}, e
	}
	if e = t.rootThread(ctx, in.ChannelID, in.ThreadTS); e != nil {
		return OperationOutput{}, e
	}
	data, e := t.uploadBytes(in)
	if e != nil {
		return OperationOutput{}, e
	}
	// Include actual bytes in the digest, so changing a path's contents cannot
	// silently reuse an operation key. Only the resulting digest is persisted.
	fingerprint := struct {
		Input         UploadInput
		ContentDigest string
	}{in, receipts.Digest(data)}
	return t.operation(a, "upload", in.OperationKey, fingerprint, func() (receipts.Receipt, error) {
		var target *slack.GetUploadURLExternalResponse
		err := slackclient.Call(ctx, func() error {
			var e error
			target, e = t.Slack.GetUploadURLExternalContext(ctx, slack.GetUploadURLExternalParameters{FileName: in.Filename, FileSize: len(data)})
			return e
		})
		if err != nil {
			return receipts.Receipt{}, err
		}
		if target == nil || !validID(target.FileID, "F") || !safeSlackFileURL(target.UploadURL, "/upload/") {
			return receipts.Receipt{}, denied()
		}
		// The URL came only from Slack and is validated before the SDK attaches auth.
		if err = t.Slack.UploadToURL(ctx, slack.UploadToURLParameters{UploadURL: target.UploadURL, Reader: bytes.NewReader(data), Filename: in.Filename}); err != nil {
			return receipts.Receipt{}, err
		}
		var result *slack.CompleteUploadExternalResponse
		err = slackclient.Call(ctx, func() error {
			var e error
			result, e = t.Slack.CompleteUploadExternalContext(ctx, slack.CompleteUploadExternalParameters{Files: []slack.FileSummary{{ID: target.FileID, Title: in.Filename}}, Channel: in.ChannelID, ThreadTimestamp: in.ThreadTS})
			return e
		})
		if err != nil {
			return receipts.Receipt{}, err
		}
		if result == nil || len(result.Files) != 1 || result.Files[0].ID != target.FileID {
			return receipts.Receipt{}, upstream()
		}
		return receipts.Receipt{ChannelID: in.ChannelID, FileID: target.FileID}, nil
	})
}
