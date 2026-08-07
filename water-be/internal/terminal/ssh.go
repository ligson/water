package terminal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const DefaultDialTimeout = 15 * time.Second

func DialSSH(ctx context.Context, profile Profile) (*ssh.Client, error) {
	auth, err := authMethods(profile)
	if err != nil {
		return nil, err
	}
	if len(auth) == 0 {
		return nil, errors.New("terminal profile has no SSH auth method")
	}

	config := &ssh.ClientConfig{
		User:            profile.Username,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback(profile.HostFingerprint),
		Timeout:         DefaultDialTimeout,
	}

	address := fmt.Sprintf("%s:%d", profile.Host, profile.Port)
	dialer := &net.Dialer{Timeout: DefaultDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dial ssh: %w", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake ssh: %w", err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func authMethods(profile Profile) ([]ssh.AuthMethod, error) {
	methods := make([]ssh.AuthMethod, 0, 2)
	if profile.AuthType == AuthTypePassword || (profile.AuthType == "" && profile.Password != "") {
		if strings.TrimSpace(profile.Password) != "" {
			methods = append(methods, ssh.Password(profile.Password))
		}
	}
	if profile.AuthType == AuthTypePrivateKey || profile.PrivateKey != "" {
		if strings.TrimSpace(profile.PrivateKey) != "" {
			signer, err := parseSigner(profile.PrivateKey, profile.Passphrase)
			if err != nil {
				return nil, err
			}
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}
	return methods, nil
}

func parseSigner(privateKey string, passphrase string) (ssh.Signer, error) {
	key := []byte(privateKey)
	if strings.TrimSpace(passphrase) != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse private key with passphrase: %w", err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return signer, nil
}

func hostKeyCallback(expected string) ssh.HostKeyCallback {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if actual != expected {
			return fmt.Errorf("ssh host fingerprint mismatch: expected %s, got %s", expected, actual)
		}
		return nil
	}
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
