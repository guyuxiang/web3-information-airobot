package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender sends HTML emails via SMTP.
type Sender struct {
	host     string
	port     int
	username string
	password string
	from     string
	fromName string
	logger   *log.Logger
}

// NewSender creates an SMTP email sender. Returns nil if required fields (host, from) are missing.
// Callers should check for nil and skip email features when not configured.
func NewSender(host string, port int, username, password, from, fromName string, logger *log.Logger) *Sender {
	if host == "" || from == "" {
		return nil
	}
	return &Sender{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
		fromName: fromName,
		logger:   logger,
	}
}

// SendDailyDigest sends an HTML daily digest to a list of recipients.
func (s *Sender) SendDailyDigest(ctx context.Context, htmlBody string, to []string) error {
	if len(to) == 0 {
		return fmt.Errorf("no recipients provided")
	}

	dateStr := time.Now().Format("2006-01-02")
	subject := fmt.Sprintf("Web3 News Daily Digest - %s", dateStr)

	// Build MIME message
	var msg strings.Builder
	fromHeader := s.from
	if s.fromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", s.fromName, s.from)
	}
	msg.WriteString(fmt.Sprintf("From: %s\r\n", fromHeader))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	msg.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	// Context-aware dial: port 465 uses direct TLS (SMTPS), others use plain + STARTTLS
	var smtpClient *smtp.Client
	var conn net.Conn
	var d net.Dialer

	if s.port == 465 {
		tlsDialer := tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: s.host}}
		tlsConn, err := tlsDialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("tls dial smtp %s: %w", addr, err)
		}
		conn = tlsConn
	} else {
		plainConn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial smtp %s: %w", addr, err)
		}
		conn = plainConn
	}

	smtpClient, err := smtp.NewClient(conn, s.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer smtpClient.Close()

	// Authenticate if credentials are provided
	if s.username != "" || s.password != "" {
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		if err := smtpClient.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	// Set sender
	if err := smtpClient.Mail(s.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}

	// Set recipients
	for _, recipient := range to {
		if err := smtpClient.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", recipient, err)
		}
	}

	// Write message body
	wc, err := smtpClient.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	_, err = wc.Write([]byte(msg.String()))
	if err != nil {
		wc.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close writer: %w", err)
	}

	// Quit
	if err := smtpClient.Quit(); err != nil {
		s.logger.Printf("smtp quit returned: %v", err)
	}

	s.logger.Printf("daily digest email sent to %d recipient(s)", len(to))
	return nil
}
