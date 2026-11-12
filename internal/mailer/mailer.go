// Package mailer sends the console's transactional email (password-reset
// links and test messages) over SMTP with stdlib only.
//
// Delivery modes: implicit TLS on port 465, STARTTLS on 587/25 (enforced
// whenever the server advertises it, refused if a public server does not
// offer TLS), and plain submission only for loopback/private relays.
package mailer

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config mirrors store.SMTPConfig minus the store layer.
type Config struct {
	Enabled  bool
	Host     string
	Port     int
	Username string
	Password string
	FromAddr string
}

// Send delivers a text message to rcpt. It returns a sender-side error
// description; message content never includes secrets.
func Send(cfg Config, rcpt, subject, body string) error {
	if !cfg.Enabled || cfg.Host == "" {
		return errors.New("mail delivery is not configured")
	}
	if cfg.FromAddr == "" {
		return errors.New("mail delivery has no From address")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	timeout := 15 * time.Second
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	host, _, _ := net.SplitHostPort(addr)
	var client *smtp.Client
	if cfg.Port == 465 {
		// Implicit TLS submission.
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return fmt.Errorf("TLS handshake failed: %w", err)
		}
		client, err = smtp.NewClient(tlsConn, host)
	} else {
		client, err = smtp.NewClient(conn, host)
	}
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP handshake failed: %w", err)
	}
	defer client.Close()

	// STARTTLS for non-465 connections when offered. Public servers that
	// offer no TLS at all are refused (credential/traffic protection);
	// loopback/private relays are allowed plain.
	if cfg.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return fmt.Errorf("STARTTLS failed: %w", err)
			}
		} else if !isPrivateHost(cfg.Host) {
			return errors.New("server does not offer STARTTLS; refusing to send credentials in the clear")
		}
	}

	var auth smtp.Auth
	if cfg.Username != "" {
		if ok, mech := client.Extension("AUTH"); ok && strings.Contains(mech, "PLAIN") {
			auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		} else if ok && strings.Contains(mech, "LOGIN") {
			auth = &loginAuth{cfg.Username, cfg.Password}
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}
	if err := client.Mail(cfg.FromAddr); err != nil {
		return fmt.Errorf("MAIL FROM rejected: %w", err)
	}
	if err := client.Rcpt(rcpt); err != nil {
		return fmt.Errorf("recipient rejected: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA rejected: %w", err)
	}
	msg := strings.Join([]string{
		"From: " + cfg.FromAddr,
		"To: " + rcpt,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("could not write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("message rejected: %w", err)
	}
	_ = client.Quit() // message handed off; a hung QUIT is not a failure
	return nil
}

type loginAuth struct {
	username, password string
}

func (a *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, errors.New("unknown SMTP LOGIN prompt")
	}
}

func isPrivateHost(host string) bool {
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		// Unresolvable names are treated as deliberate operator choices;
		// the send itself will fail loudly if unreachable.
		return true
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() {
			return true
		}
	}
	return false
}
