// Package userinfo looks up the account details Tilde needs: IDs, groups,
// home directory and login shell.
package userinfo

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
)

// Info describes a local account.
type Info struct {
	Name   string
	UID    uint32
	GID    uint32
	Groups []uint32
	Home   string
	Shell  string
}

// Lookup returns account details for name.
func Lookup(name string) (*Info, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, err
	}
	return fromUser(u)
}

// Current returns account details for the user running this process.
func Current() (*Info, error) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return nil, err
	}
	return fromUser(u)
}

func fromUser(u *user.User) (*Info, error) {
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse uid %q: %w", u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}
	info := &Info{Name: u.Username, UID: uint32(uid), GID: uint32(gid), Home: u.HomeDir}
	if ids, err := u.GroupIds(); err == nil {
		for _, g := range ids {
			if n, err := strconv.ParseUint(g, 10, 32); err == nil {
				info.Groups = append(info.Groups, uint32(n))
			}
		}
	}
	info.Shell = shellFor(u.Username)
	return info, nil
}

// shellFor reads the login shell from /etc/passwd, then getent (for NSS
// accounts such as LDAP), and falls back to /bin/sh.
func shellFor(name string) string {
	if s := shellFromPasswd("/etc/passwd", name); s != "" {
		return s
	}
	if out, err := exec.Command("getent", "passwd", name).Output(); err == nil {
		if s := shellFromLine(strings.TrimSpace(string(out)), name); s != "" {
			return s
		}
	}
	return "/bin/sh"
}

func shellFromPasswd(path, name string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if s := shellFromLine(sc.Text(), name); s != "" {
			return s
		}
	}
	return ""
}

func shellFromLine(line, name string) string {
	fields := strings.Split(line, ":")
	if len(fields) < 7 || fields[0] != name {
		return ""
	}
	return fields[6]
}
