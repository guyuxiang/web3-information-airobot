package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"aiweb3news/internal/analysis"
	"aiweb3news/internal/config"
	"aiweb3news/internal/email"
	"aiweb3news/internal/rss"
	"aiweb3news/internal/storage"
)

const wecomWebhookURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=%s"

// Service ties together RSS polling and AI analysis.
type Service struct {
	fetcher     *rss.Fetcher
	analyzer    analysis.Analyzer
	store       *storage.Store
	emailSender *email.Sender
	logger      *log.Logger
	cfg         config.Config
}

// NewService creates a Service instance.
func NewService(fetcher *rss.Fetcher, analyzer analysis.Analyzer, store *storage.Store, emailSender *email.Sender, logger *log.Logger, cfg config.Config) *Service {
	return &Service{
		fetcher:     fetcher,
		analyzer:    analyzer,
		store:       store,
		emailSender: emailSender,
		logger:      logger,
		cfg:         cfg,
	}
}

// Run starts the HTTP server and the polling loop.
func (s *Service) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthHandler)
	mux.HandleFunc("/items", s.itemsHandler)

	srv := &http.Server{
		Addr:    s.cfg.BindAddr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		s.logger.Printf("HTTP server listening on %s", s.cfg.BindAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("http server error: %v", err)
		}
	}()

	// Kick off an initial fetch.
	s.pollOnce(ctx)

	// Start daily email digest loop.
	go s.startDailyEmailLoop(ctx)

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Println("stopping service, context cancelled")
			return nil
		case <-ticker.C:
			s.pollOnce(ctx)
		}
	}
}

func (s *Service) pollOnce(ctx context.Context) {
	s.logger.Println("polling once")
	items, err := s.fetcher.Fetch(ctx)
	if err != nil {
		s.logger.Printf("failed to fetch feed: %v", err)
		return
	}

	for _, item := range items {
		exists, err := s.store.Exists(ctx, item.GUID)
		if err != nil {
			s.logger.Printf("check exists failed for %s: %v", item.GUID, err)
			continue
		}
		if exists {
			continue
		}

		result, err := s.analyzer.Evaluate(ctx, analysis.ItemContext{
			Title:       item.Title,
			Link:        item.Link,
			PublishedAt: item.PublishedAt,
			Summary:     item.Description,
		})
		if err != nil {
			s.logger.Printf("analysis error for %s: %v", item.Title, err)
			continue
		}

		if err := s.store.SaveAnalysis(ctx, item, result); err != nil {
			s.logger.Printf("store analysis failed for %s: %v", item.Title, err)
			continue
		}

		if result.Relevant {
			s.notifyWebhook(ctx, item, result)
		}
	}
}

func (s *Service) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Service) itemsHandler(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRelevant(r.Context(), s.cfg.MaxItems)
	if err != nil {
		s.logger.Printf("list relevant failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Count int                  `json:"count"`
		Items []storage.StoredItem `json:"items"`
	}{
		Count: len(items),
		Items: items,
	}); err != nil {
		s.logger.Printf("write items response failed: %v", err)
	}
}

func (s *Service) notifyWebhook(ctx context.Context, item rss.Item, result analysis.Result) {
	if s.cfg.WecomWebhookKey == "" {
		return
	}
	payload := map[string]any{
		"msgtype": "text",
		"text": map[string]string{
			"content": fmt.Sprintf("%s\n%s", item.Title, item.Link),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.Printf("marshal webhook payload failed: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(wecomWebhookURL, s.cfg.WecomWebhookKey), bytes.NewReader(body))
	if err != nil {
		s.logger.Printf("build webhook request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Printf("send webhook failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		s.logger.Printf("webhook returned non-2xx status: %s", resp.Status)
	}
}

// startDailyEmailLoop schedules the daily digest email at the configured hour (Beijing time).
func (s *Service) startDailyEmailLoop(ctx context.Context) {
	if s.emailSender == nil {
		s.logger.Println("email sender not configured, skipping daily digest")
		return
	}

	beijingLoc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		s.logger.Printf("failed to load Asia/Shanghai location: %v, falling back to local", err)
		beijingLoc = time.Now().Location()
	}

	now := time.Now().In(beijingLoc)
	next := time.Date(now.Year(), now.Month(), now.Day(), s.cfg.EmailSendHour, 0, 0, 0, beijingLoc)

	// If the send hour today has already passed, schedule for tomorrow.
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}

	duration := next.Sub(time.Now())
	s.logger.Printf("daily email digest scheduled at %s Beijing time (in %s)", next.Format("2006-01-02 15:04:05"), duration.Round(time.Minute))

	// First trigger: one-shot timer to align to the exact hour.
	timer := time.NewTimer(duration)

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.sendDailyEmail(ctx)

			// Next trigger in 24 hours.
			timer.Reset(24 * time.Hour)
		}
	}
}

// sendDailyEmail queries yesterday's items and sends the digest email.
func (s *Service) sendDailyEmail(ctx context.Context) {
	s.logger.Println("preparing daily digest email")

	beijingLoc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		beijingLoc = time.Now().Location()
	}
	now := time.Now().In(beijingLoc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, beijingLoc)
	yesterday := today.Add(-24 * time.Hour)

	items, err := s.store.ListRelevantByDateRange(ctx, yesterday, today)
	if err != nil {
		s.logger.Printf("failed to query items for daily digest: %v", err)
		return
	}

	if len(items) == 0 {
		s.logger.Println("no relevant items for yesterday, skipping daily digest")
		return
	}

	htmlBody := email.BuildDailyDigestHTML(items, yesterday)
	if htmlBody == "" {
		s.logger.Println("daily digest HTML builder returned empty, skipping send")
		return
	}

	// Parse recipients (comma-separated)
	recipients := strings.Split(s.cfg.EmailTo, ",")
	var cleanRecipients []string
	for _, r := range recipients {
		r = strings.TrimSpace(r)
		if r != "" {
			cleanRecipients = append(cleanRecipients, r)
		}
	}

	if len(cleanRecipients) == 0 {
		s.logger.Println("no valid EMAIL_TO recipients, skipping daily digest")
		return
	}

	if err := s.emailSender.SendDailyDigest(ctx, htmlBody, cleanRecipients); err != nil {
		s.logger.Printf("failed to send daily digest: %v", err)
		return
	}

	s.logger.Printf("daily digest email sent to %d recipient(s) with %d items", len(cleanRecipients), len(items))
}
