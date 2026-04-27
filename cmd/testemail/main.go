package main

import (
	"context"
	"log"
	"os"
	"time"

	"aiweb3news/internal/config"
	"aiweb3news/internal/email"
	"aiweb3news/internal/storage"
)

func main() {
	logger := log.New(os.Stdout, "[testemail] ", log.LstdFlags)
	ctx := context.Background()

	cfg := config.Load()
	store, err := storage.NewMySQLStore(ctx, cfg, logger)
	if err != nil {
		logger.Fatalf("failed to connect to mysql: %v", err)
	}
	defer store.Close()

	// Fetch relevant items from the past 7 days
	now := time.Now()
	weekAgo := now.Add(-7 * 24 * time.Hour)
	items, err := store.ListRelevantByDateRange(ctx, weekAgo, now)
	if err != nil {
		logger.Fatalf("failed to list items: %v", err)
	}

	if len(items) == 0 {
		// Fallback: get latest items without date filter
		items, err = store.ListRelevant(ctx, 10)
		if err != nil {
			logger.Fatalf("failed to list all items: %v", err)
		}
	}

	sender := email.NewSender(
		cfg.EmailSMTPHost, cfg.EmailSMTPPort,
		cfg.EmailSMTPUser, cfg.EmailSMTPPass,
		cfg.EmailFrom, cfg.EmailFromName,
		logger,
	)
	if sender == nil {
		logger.Fatal("email sender not configured (missing SMTP host or from address)")
	}

	htmlBody := email.BuildDailyDigestHTML(items, now)
	if htmlBody == "" {
		logger.Fatal("no items to send")
	}

	recipient := "guyuxiang@linklogis.com"
	if err := sender.SendDailyDigest(ctx, htmlBody, []string{recipient}); err != nil {
		logger.Fatalf("failed to send email: %v", err)
	}

	logger.Printf("test email sent to %s with %d items", recipient, len(items))
}
