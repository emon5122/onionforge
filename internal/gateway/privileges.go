package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// prepareFilesystem creates required directories, fixes ownership and
// permissions, and drops root privileges. Tor and Caddy never run as root.
func prepareFilesystem(p Paths, log *Logger) error {
	if os.Geteuid() == 0 {
		return prepareAsRoot(p, log)
	}
	return prepareAsUser(p)
}

func prepareAsRoot(p Paths, log *Logger) error {
	u, err := user.Lookup(p.User)
	if err != nil {
		return fmt.Errorf("running as root but user %q does not exist (set ONIONFORGE_USER): %w", p.User, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if uid == 0 {
		return fmt.Errorf("ONIONFORGE_USER %q must not be root", p.User)
	}

	for _, d := range []string{p.DataDir, p.RunDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}
	fixed := 0
	err = filepath.WalkDir(p.DataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && (int(st.Uid) != uid || int(st.Gid) != gid) {
			fixed++
			return os.Lchown(path, uid, gid)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("fixing ownership of %s: %w", p.DataDir, err)
	}
	if fixed > 0 {
		log.Infof("Adjusted ownership of %d path(s) in %s to %s", fixed, p.DataDir, p.User)
	}
	if err := os.Chown(p.RunDir, uid, gid); err != nil {
		return err
	}
	if err := os.Chmod(p.DataDir, 0o700); err != nil {
		return err
	}

	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("dropping privileges: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("dropping privileges: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("dropping privileges: %w", err)
	}
	os.Setenv("HOME", p.CaddyHome())
	os.Setenv("USER", p.User)
	return nil
}

func prepareAsUser(p Paths) error {
	for _, d := range []string{p.DataDir, p.RunDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w (when running as a non-root user, the volume must be writable by uid %d)", d, err, os.Geteuid())
		}
	}
	info, err := os.Stat(p.DataDir)
	if err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d but OnionForge runs as uid %d; Tor requires its directories to be owned by the running user (chown the volume, or start the container as root and let OnionForge fix it)",
			p.DataDir, st.Uid, os.Geteuid())
	}
	probe := filepath.Join(p.DataDir, ".write-test")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return fmt.Errorf("%s is not writable: %w", p.DataDir, err)
	}
	os.Remove(probe)
	return os.Chmod(p.DataDir, 0o700)
}

// fixIdentityPermissions enforces the modes Tor requires on a service dir.
func fixIdentityPermissions(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := os.Chmod(filepath.Join(dir, e.Name()), 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
