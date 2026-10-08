package identity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultVanityBinary is the vanity generator bundled in the image
// (github.com/offset/onion-vanity-address).
const DefaultVanityBinary = "onion-vanity-address"

// GenerateVanity runs the bundled vanity generator for prefix and returns
// the resulting keypair. The generator's output is not trusted blindly: the
// public key and hostname are re-derived from the secret key and the prefix
// is checked.
func GenerateVanity(ctx context.Context, binary, prefix string) (*Keypair, error) {
	if binary == "" {
		binary = DefaultVanityBinary
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, prefix)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("vanity generator failed: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("running vanity generator %q: %w", binary, err)
	}
	defer clear(stdout.Bytes())

	var encoded string
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), SecretKeyFile+":"); ok {
			encoded = strings.TrimSpace(v)
		}
	}
	if encoded == "" {
		return nil, errors.New("vanity generator produced no secret key")
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("vanity generator produced a malformed secret key")
	}
	defer clear(secret)
	kp, err := ParseSecretKey(secret)
	if err != nil {
		return nil, fmt.Errorf("vanity generator output: %w", err)
	}
	if !HasPrefix(kp.Hostname, prefix) {
		return nil, fmt.Errorf("vanity generator returned %s, which does not start with %q", kp.Hostname, prefix)
	}
	return kp, nil
}
