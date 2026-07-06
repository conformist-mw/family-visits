package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"visits/internal/bot"
	"visits/internal/db"
	"visits/internal/parse"
	"visits/internal/store"
)

func main() {
	dbFlag := flag.String("db", "", "SQLite database path (overrides APP_DB)")
	flag.Parse()

	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	loc := loadLocation(os.Getenv("APP_TZ"), logger)

	dbPath := firstNonEmpty(*dbFlag, os.Getenv("APP_DB"), "data/visits.db")
	database, err := db.Open(dbPath)
	if err != nil {
		logger.Error("open db", "err", err)
		os.Exit(1)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			logger.Error("GEMINI_API_KEY is required when the bot is enabled")
			os.Exit(1)
		}
		modelName := firstNonEmpty(os.Getenv("GEMINI_MODEL"), "gemini-flash-lite-latest")
		parser, err := parse.New(ctx, apiKey, modelName, loc, splitCSV(os.Getenv("VISIT_PEOPLE")))
		if err != nil {
			logger.Error("parser init", "err", err)
			os.Exit(1)
		}

		notifyChat, _ := strconv.ParseInt(os.Getenv("TELEGRAM_NOTIFY_CHAT"), 10, 64)
		cfg := bot.Config{
			Token:            token,
			WebhookURL:       os.Getenv("TELEGRAM_WEBHOOK_URL"),
			WebhookSecret:    os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
			AllowedChats:     bot.ParseChatIDs(os.Getenv("TELEGRAM_ALLOWED_CHATS"), logger),
			NotifyChat:       notifyChat,
			Loc:              loc,
			DailyDigestTime:  os.Getenv("DAILY_DIGEST_TIME"),
			WeeklyDigestDOW:  parseDOW(os.Getenv("WEEKLY_DIGEST_DOW")),
			WeeklyDigestTime: os.Getenv("WEEKLY_DIGEST_TIME"),
		}
		vbot, err := bot.New(cfg, store.New(database), parser, logger)
		if err != nil {
			logger.Error("bot init", "err", err)
			os.Exit(1)
		}
		defer vbot.Stop()

		if vbot.WebhookMode() {
			mux.Handle(vbot.WebhookPath(), vbot.WebhookHandler())
			if err := vbot.RegisterWebhook(); err != nil {
				logger.Error("bot: register webhook", "err", err)
			} else {
				logger.Info("bot: webhook registered")
			}
		} else {
			go vbot.RunPolling(ctx)
		}
		go vbot.RunScheduler(ctx)
	} else {
		logger.Info("bot: disabled (TELEGRAM_BOT_TOKEN not set)")
	}

	addr := firstNonEmpty(os.Getenv("ADDR"), ":8090")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logger.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

func loadLocation(name string, logger *slog.Logger) *time.Location {
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		logger.Warn("bad APP_TZ, using Local", "value", name, "err", err)
		return time.Local
	}
	return loc
}

// parseDOW returns 0..6 for a valid day-of-week, or -1 (disabled) otherwise.
func parseDOW(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 6 {
		return -1
	}
	return n
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
