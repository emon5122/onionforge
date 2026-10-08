package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test vector published in the onion-vanity-address README.
const (
	vectorSecret   = "PT0gZWQyNTUxOXYxLXNlY3JldDogdHlwZTAgPT0AAABI0dtnOsAw0DPamQtZx/6G83IUxdByrnWakYApdO/QcJaicGyBL8wKTwbqMDsLgyZJigdTqfvUyWDxWU3mVPF/"
	vectorPublic   = "PT0gZWQyNTUxOXYxLXB1YmxpYzogdHlwZTAgPT0AAAA6BjI2vao8Rv7ra2WzJ9YG4dyeEaM8jLfHjZsbbKh6wg=="
	vectorHostname = "hiddenv5vi6en7xlnns3gj6wa3q5zhqrum6izn6hrwnrw3fiplbgpjyd.onion"
)

func decode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSecretKeyVector(t *testing.T) {
	kp, err := ParseSecretKey(decode(t, vectorSecret))
	if err != nil {
		t.Fatal(err)
	}
	if kp.Hostname != vectorHostname {
		t.Errorf("hostname = %s, want %s", kp.Hostname, vectorHostname)
	}
	if string(kp.public) != string(decode(t, vectorPublic)) {
		t.Error("public key mismatch")
	}
	if strings.Contains(kp.String(), "secret") || strings.Contains(kp.String(), vectorSecret) {
		t.Error("String() must not leak key material")
	}
}

func TestParseSecretKeyRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("hello"), append([]byte(secretKeyHeader), make([]byte, 10)...)} {
		if _, err := ParseSecretKey(b); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}

func TestGenerateWriteLoad(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root, "api")
	if _, err := Load("api", dir); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load on empty = %v, want ErrNotFound", err)
	}
	kp, err := GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	if len(kp.Hostname) != 62 || !strings.HasSuffix(kp.Hostname, "d.onion") {
		t.Errorf("unexpected hostname %q", kp.Hostname)
	}
	if err := Write(dir, kp); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{".": 0o700, SecretKeyFile: 0o600, PublicKeyFile: 0o600} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", name, st.Mode().Perm(), mode)
		}
	}
	id, err := Load("api", dir)
	if err != nil {
		t.Fatal(err)
	}
	if id.Hostname != kp.Hostname {
		t.Errorf("reloaded hostname %s != %s", id.Hostname, kp.Hostname)
	}

	// Never overwrite an existing identity.
	kp2, _ := GenerateRandom()
	if err := Write(dir, kp2); err == nil {
		t.Fatal("Write over existing identity must fail")
	}
	if id2, _ := Load("api", dir); id2.Hostname != kp.Hostname {
		t.Fatal("identity changed after refused overwrite")
	}
	// No temp directories left behind.
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Errorf("unexpected leftovers in %s: %v", root, entries)
	}
}

func TestWriteIntoEmptyDir(t *testing.T) {
	dir := Dir(t.TempDir(), "api")
	os.Mkdir(dir, 0o755)
	kp, _ := GenerateRandom()
	if err := Write(dir, kp); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPartialIdentityRefused(t *testing.T) {
	dir := Dir(t.TempDir(), "api")
	os.Mkdir(dir, 0o700)
	os.WriteFile(filepath.Join(dir, HostnameFile), []byte(vectorHostname+"\n"), 0o600)
	_, err := Load("api", dir)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("partial identity must be an error, got %v", err)
	}
}

func TestLoadMismatchedPublicKey(t *testing.T) {
	dir := Dir(t.TempDir(), "api")
	kp, _ := GenerateRandom()
	if err := Write(dir, kp); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, PublicKeyFile), decode(t, vectorPublic), 0o600)
	if _, err := Load("api", dir); err == nil {
		t.Fatal("mismatched public key must be an error")
	}
}

func fakeGenerator(t *testing.T, secret string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gen")
	script := "#!/bin/sh\necho 'Found ...' >&2\necho ---\necho 'hostname: ignored.onion'\necho 'hs_ed25519_secret_key: " + secret + "'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGenerateVanity(t *testing.T) {
	kp, err := GenerateVanity(context.Background(), VanityOptions{Binary: fakeGenerator(t, vectorSecret), Prefixes: []string{"hidden"}})
	if err != nil {
		t.Fatal(err)
	}
	if kp.Hostname != vectorHostname {
		t.Errorf("hostname = %s", kp.Hostname)
	}
	// Output that does not match the requested prefix is rejected.
	if _, err := GenerateVanity(context.Background(), VanityOptions{Binary: fakeGenerator(t, vectorSecret), Prefixes: []string{"other"}}); err == nil {
		t.Error("expected prefix mismatch error")
	}
	// Any of several alternatives is accepted.
	if _, err := GenerateVanity(context.Background(), VanityOptions{Binary: fakeGenerator(t, vectorSecret), Prefixes: []string{"other", "hidd"}}); err != nil {
		t.Errorf("alternative prefix rejected: %v", err)
	}
	if _, err := GenerateVanity(context.Background(), VanityOptions{Binary: fakeGenerator(t, "!!!"), Prefixes: []string{"hidden"}}); err == nil {
		t.Error("expected malformed output error")
	}
}

func TestBenchmarkVanity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gen")
	os.WriteFile(p, []byte("#!/bin/sh\necho 'Stopped searching [zz]... after 5s and 670000000 attempts (134000000 attempts/s)' >&2\nexit 2\n"), 0o755)
	rate, err := BenchmarkVanity(context.Background(), VanityOptions{Binary: p}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 134000000 {
		t.Errorf("rate = %v", rate)
	}
}

func TestEstimateVanity(t *testing.T) {
	// 8 characters at 134M keys/s: mean 32^8/134e6 ≈ 8205 s.
	est := EstimateVanity([]string{"nexisltd"}, 134e6)
	mean := math.Pow(32, 8) / 134e6
	if d := est.Median.Seconds() - math.Ln2*mean; math.Abs(d) > 1 {
		t.Errorf("median = %v", est.Median)
	}
	if est.P90 <= est.Median {
		t.Error("p90 must exceed median")
	}
	// Two alternatives of equal length halve the expected time.
	two := EstimateVanity([]string{"nexisltd", "nexisweb"}, 134e6)
	if r := est.Median.Seconds() / two.Median.Seconds(); math.Abs(r-2) > 0.01 {
		t.Errorf("two prefixes speedup = %v", r)
	}
	for d, want := range map[time.Duration]string{30 * time.Second: "30 s", 45 * time.Minute: "45 min", 3 * time.Hour: "3.0 hours", 72 * time.Hour: "3.0 days"} {
		if got := HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
