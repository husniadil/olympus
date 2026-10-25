package backendtest

import "testing"

// The values are what POSIX cksum prints for the same input, so the case that
// reads a shell's cksum compares like with like.
func TestCksumMatchesPOSIXCksum(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint32
	}{
		{"", 4294967295},
		{"abc", 1219131554},
		{largeText(1000, 7), 224856346},
	} {
		if got := cksum([]byte(c.in)); got != c.want {
			t.Errorf("cksum(%.10q) = %d, want %d", c.in, got, c.want)
		}
	}
}
