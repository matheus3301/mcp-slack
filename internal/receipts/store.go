// Package receipts stores only operation digests and delivery identifiers. A
// durable pending claim is written before any send; crash/timeout never frees it.
package receipts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

var ErrUncertain = errors.New("OPERATION_UNCERTAIN: receipt is pending; reconcile before using a new key")
var ErrConflict = errors.New("OPERATION_CONFLICT: operation key was used with different arguments")
var ErrStore = errors.New("STATE_UNAVAILABLE: protected receipt storage is unavailable")

type Receipt struct {
	ChannelID          string `json:"channel_id"`
	TS                 string `json:"ts,omitempty"`
	ScheduledMessageID string `json:"scheduled_message_id,omitempty"`
	FileID             string `json:"file_id,omitempty"`
}
type record struct {
	Digest  string  `json:"digest"`
	Receipt Receipt `json:"receipt"`
}
type Store struct{ root *os.Root }

func Open(path string) (*Store, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrStore
	}
	if !filepath.IsAbs(path) {
		return nil, ErrStore
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, ErrStore
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !owned(info, false) {
		return nil, ErrStore
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrStore
	}
	// Sync the entire creation chain: a durable pending file is insufficient
	// if a newly created ancestor directory could disappear after a crash.
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		f, e := os.Open(dir)
		if e != nil {
			_ = root.Close() // The storage failure remains the reported error.
			return nil, ErrStore
		}
		e = f.Sync()
		ce := f.Close()
		if e != nil || ce != nil {
			_ = root.Close()
			return nil, ErrStore
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return &Store{root: root}, nil
}
func (s *Store) Close() error { return s.root.Close() }
func Digest(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (s *Store) read(name string) (record, error) {
	var r record
	fi, e := s.root.Lstat(name)
	if e != nil {
		return r, e
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 || fi.Size() > 4096 || !owned(fi, true) {
		return r, ErrStore
	}
	f, e := s.root.Open(name)
	if e != nil {
		return r, ErrStore
	}
	defer f.Close()
	if e = json.NewDecoder(io.LimitReader(f, 4097)).Decode(&r); e != nil {
		return r, ErrStore
	}
	return r, nil
}

// Begin atomically reserves a key across processes. completed is true only for
// an identical, previously completed operation. Never automatically expire claims.
func (s *Store) Begin(namespace, key, digest string) (result Receipt, completed bool, err error) {
	name := Digest([]byte(namespace + "\x00" + key))
	if r, e := s.read(name + ".done"); e == nil {
		if r.Digest != digest {
			return result, false, ErrConflict
		}
		return r.Receipt, true, nil
	} else if !os.IsNotExist(e) {
		return result, false, ErrStore
	}
	f, e := s.root.OpenFile(name+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(e) {
		r, re := s.read(name + ".pending")
		if re == nil && r.Digest != digest {
			return result, false, ErrConflict
		}
		return result, false, ErrUncertain
	}
	if e != nil {
		return result, false, ErrStore
	}
	data, e := json.Marshal(record{Digest: digest})
	if e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return result, false, ErrStore
	}
	if e = s.syncDir(); e != nil {
		return result, false, e
	}
	return result, false, nil
}
func (s *Store) syncDir() error {
	f, e := s.root.Open(".")
	if e != nil {
		return ErrStore
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrStore
	}
	return nil
}
func (s *Store) Complete(namespace, key, digest string, result Receipt) error {
	name := Digest([]byte(namespace + "\x00" + key))
	r, e := s.read(name + ".pending")
	if e != nil || r.Digest != digest {
		return ErrStore
	}
	// A separate exclusively-created completion file means an incomplete write
	// remains a denial, never a reason to resend. No rename can overwrite a peer.
	f, e := s.root.OpenFile(name+".done", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ErrStore
	}
	data, e := json.Marshal(record{Digest: digest, Receipt: result})
	if e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return ErrStore
	}
	return s.syncDir()
}
