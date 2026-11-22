package mcp

// audit: behavior - file roots decode to host paths, including spaces, localhost, Windows drives, and UNC shares.
// audit: regression - a remote or UNC root could bind to a local path or the wrong drive.
// audit: coverage gap - filePath was untested for non-local hosts and Windows forms.
// audit: seam - the test calls filePathForOS with an explicit GOOS.

import "testing"

func TestFilePathForOS(t *testing.T) {
	cases := []struct {
		goos string
		uri  string
		want string
		ok   bool
	}{
		{"linux", "file:///home/u/a%20b", "/home/u/a b", true},
		{"linux", "file://localhost/home/u", "/home/u", true},
		{"linux", "file://remote/etc/passwd", "", false},
		{"windows", "file:///C:/Users/u", `C:\Users\u`, true},
		{"windows", "file://server/share/dir", `\\server\share\dir`, true},
	}
	for _, testCase := range cases {
		got, ok := filePathForOS(testCase.goos, testCase.uri)
		if got != testCase.want || ok != testCase.ok {
			t.Errorf("filePathForOS(%q, %q) = %q, %v; want %q, %v",
				testCase.goos, testCase.uri, got, ok, testCase.want, testCase.ok)
		}
	}
}
