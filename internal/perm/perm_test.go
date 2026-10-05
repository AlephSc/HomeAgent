package perm

import (
	"reflect"
	"testing"
)

// G1a: tokenizer sadar-kutip — pemisah di dalam kutip bukan pemisah segmen.
func TestSplitSegmentsQuoteAware(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{
			in:   `grep -E "hostapd|dnsmasq" /etc/x.conf`,
			want: []string{`grep -E "hostapd|dnsmasq" /etc/x.conf`},
		},
		{
			in:   `ps aux | grep -E "upstream|dns" | grep -v grep`,
			want: []string{`ps aux`, `grep -E "upstream|dns"`, `grep -v grep`},
		},
		{
			in:   `echo "a && b"; ls -la`,
			want: []string{`echo "a && b"`, `ls -la`},
		},
		{
			in:   `echo 'hi || bye'`,
			want: []string{`echo 'hi || bye'`},
		},
		{
			in:   ``,
			want: nil,
		},
	}
	for _, c := range cases {
		got := splitSegments(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitSegments(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// G1a: head dari command berkutip.
func TestHeadsQuoteAware(t *testing.T) {
	got := Heads(`grep -E "hostapd|dnsmasq" x.conf`)
	want := []string{"grep"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Heads = %v, want %v", got, want)
	}
}
