package frontend

import "testing"

func TestSRTCallerURL(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"srt://node:8890?streamid=read:cam&latency=120000", "srt://node:8890?latency=120&streamid=read%3Acam"},
		{"srt://node:8890?mode=listener", ""},
		{"srt://node:0", ""},
		{"http://node:8890", ""},
	} {
		got, err := srtCallerURL(tc.url)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("srtCallerURL(%q) = %q, %v", tc.url, got, err)
		}
	}
}
