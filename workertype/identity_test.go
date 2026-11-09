package workertype

import (
	"strings"
	"testing"
)

func TestCheckIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, id, version, digest, want string
	}{
		{name: "full", id: "com.example.sap-s4", version: "2.1.3", digest: digest},
		{name: "no digest", id: "io.atlas.wikiwikibangbang", version: "0.24.0-rc.1"},
		{name: "no id", version: "1.0.0", want: "id is required"},
		{name: "not dotted", id: "jira", version: "1.0.0", want: "reverse-DNS"},
		{name: "upper case", id: "Com.Example.X", version: "1.0.0", want: "reverse-DNS"},
		{name: "too long id", id: "a." + strings.Repeat("b", 130), version: "1.0.0", want: "reverse-DNS"},
		{name: "no version", id: "com.example.x", want: "version is required"},
		{name: "not semver", id: "com.example.x", version: "v1", want: "not a semantic version"},
		{name: "too long version", id: "com.example.x", version: "1.0.0-" + strings.Repeat("a", 70), want: "longer than"},
		{name: "bad digest", id: "com.example.x", version: "1.0.0", digest: "md5:abc", want: "sha256:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckIdentity(tc.id, tc.version, tc.digest)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckIdentity = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckIdentity = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}
