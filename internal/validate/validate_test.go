package validate

import "testing"

func TestChannelID(t *testing.T) {
	t.Parallel()
	valid := []string{"C01234567", "G0ABCDEFG", "CABCDEF12", "GZZZZZZ99"}
	for _, id := range valid {
		if err := ChannelID(id); err != nil {
			t.Errorf("ChannelID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",                             // empty
		"D01234567",                    // DM
		"U01234567",                    // user
		"c01234567",                    // lowercase prefix
		"C0123",                        // too short
		"C" + string(make([]byte, 40)), // too long / junk
		"C0123456!",                    // bad char
		"channel-name",                 // name not ID
		"CABCDEF 12",                   // space
	}
	for _, id := range invalid {
		if err := ChannelID(id); err == nil {
			t.Errorf("ChannelID(%q) = nil, want error", id)
		}
	}
}

func TestTimestamp(t *testing.T) {
	t.Parallel()
	if err := Timestamp(""); err != nil {
		t.Errorf("empty optional timestamp should be valid, got %v", err)
	}
	if err := Timestamp("1699999999.123456"); err != nil {
		t.Errorf("valid ts rejected: %v", err)
	}
	for _, bad := range []string{"169999.12", "1699999999", "1699999999.12", "abc.def", "1699999999.1234567"} {
		if err := Timestamp(bad); err == nil {
			t.Errorf("Timestamp(%q) = nil, want error", bad)
		}
	}
}

func TestRequiredTimestamp(t *testing.T) {
	t.Parallel()
	if err := RequiredTimestamp(""); err == nil {
		t.Error("empty required timestamp should error")
	}
	if err := RequiredTimestamp("1699999999.123456"); err != nil {
		t.Errorf("valid required ts rejected: %v", err)
	}
}

func TestCursor(t *testing.T) {
	t.Parallel()
	if err := Cursor(""); err != nil {
		t.Errorf("empty cursor should be valid, got %v", err)
	}
	if err := Cursor("dGVhbTpDMDYxRkE1UEI="); err != nil {
		t.Errorf("valid base64 cursor rejected: %v", err)
	}
	long := make([]byte, maxCursorLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := Cursor(string(long)); err == nil {
		t.Error("overlong cursor should error")
	}
	for _, bad := range []string{"has space", "curly{}", "semi;colon"} {
		if err := Cursor(bad); err == nil {
			t.Errorf("Cursor(%q) = nil, want error", bad)
		}
	}
}

func TestLimit(t *testing.T) {
	t.Parallel()
	got, err := Limit(0)
	if err != nil || got != DefaultLimit {
		t.Errorf("Limit(0) = (%d,%v), want (%d,nil)", got, err, DefaultLimit)
	}
	for _, ok := range []int{1, 50, 100} {
		if got, err := Limit(ok); err != nil || got != ok {
			t.Errorf("Limit(%d) = (%d,%v), want (%d,nil)", ok, got, err, ok)
		}
	}
	for _, bad := range []int{-1, 101, 1000} {
		if _, err := Limit(bad); err == nil {
			t.Errorf("Limit(%d) = nil error, want error", bad)
		}
	}
}
