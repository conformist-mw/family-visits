package bot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"visits/internal/parse"
	"visits/internal/store"
)

type Config struct {
	Token         string
	WebhookURL    string
	WebhookSecret string
	AllowedChats  []int64
	NotifyChat    int64 // where digests are pushed; 0 disables the scheduler

	Loc              *time.Location
	DailyDigestTime  string // "HH:MM" in Loc; "" disables
	WeeklyDigestDOW  int    // 0=Sun..6=Sat; <0 disables
	WeeklyDigestTime string // "HH:MM" in Loc
}

type Bot struct {
	b       *tele.Bot
	cfg     Config
	store   *store.Store
	parser  *parse.Parser
	logger  *slog.Logger
	allowed map[int64]bool

	pending  *pendingStore
	awaiting *awaitingStore
}

// ParseChatIDs parses a comma-separated list of int64 chat ids.
func ParseChatIDs(raw string, logger *slog.Logger) []int64 {
	var out []int64
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			if logger != nil {
				logger.Warn("bot: bad chat id in TELEGRAM_ALLOWED_CHATS", "value", p)
			}
			continue
		}
		out = append(out, id)
	}
	return out
}

func New(cfg Config, st *store.Store, parser *parse.Parser, logger *slog.Logger) (*Bot, error) {
	pref := tele.Settings{Token: cfg.Token}
	if cfg.WebhookURL == "" {
		pref.Poller = &tele.LongPoller{Timeout: 10 * time.Second}
	}

	tb, err := tele.NewBot(pref)
	if err != nil {
		return nil, err
	}

	bot := &Bot{
		b:        tb,
		cfg:      cfg,
		store:    st,
		parser:   parser,
		logger:   logger,
		allowed:  make(map[int64]bool, len(cfg.AllowedChats)),
		pending:  newPendingStore(),
		awaiting: newAwaitingStore(),
	}
	for _, id := range cfg.AllowedChats {
		bot.allowed[id] = true
	}

	tb.Use(bot.authMiddleware)

	tb.Handle("/start", bot.cmdStart)
	tb.Handle("/help", bot.cmdStart)
	tb.Handle("/visit", bot.cmdVisit)
	tb.Handle("/list", bot.cmdList)
	tb.Handle("/week", bot.cmdWeek)
	tb.Handle(tele.OnText, bot.onText)

	tb.Handle(&tele.Btn{Unique: "appt_save"}, bot.onSave)
	tb.Handle(&tele.Btn{Unique: "appt_cancel"}, bot.onCancel)
	tb.Handle(&tele.Btn{Unique: "appt_resched"}, bot.onReschedule)
	tb.Handle(&tele.Btn{Unique: "appt_del"}, bot.onCancelAppt)

	// Populate the "/" command menu (best-effort; a network hiccup here must
	// not block startup).
	if err := tb.SetCommands([]tele.Command{
		{Text: "visit", Description: "Добавить визит: /visit завтра 15:00 педикюр"},
		{Text: "week", Description: "Что на ближайшую неделю"},
		{Text: "list", Description: "Все визиты (перенос/отмена)"},
		{Text: "help", Description: "Как пользоваться"},
	}); err != nil {
		logger.Warn("bot: set commands", "err", err)
	}

	return bot, nil
}

func (b *Bot) WebhookMode() bool { return b.cfg.WebhookURL != "" }

func (b *Bot) WebhookPath() string {
	u, err := url.Parse(b.cfg.WebhookURL)
	if err != nil || u.Path == "" || u.Path == "/" {
		return "/tgwebhook"
	}
	return u.Path
}

func (b *Bot) RunPolling(ctx context.Context) {
	go func() {
		<-ctx.Done()
		b.b.Stop()
	}()
	b.logger.Info("bot: starting polling")
	b.b.Start()
}

func (b *Bot) Stop() {
	if b.b != nil {
		b.b.Stop()
	}
}

func (b *Bot) RegisterWebhook() error {
	w := &tele.Webhook{
		Endpoint:    &tele.WebhookEndpoint{PublicURL: b.cfg.WebhookURL},
		SecretToken: b.cfg.WebhookSecret,
	}
	return b.b.SetWebhook(w)
}

func (b *Bot) WebhookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b.cfg.WebhookSecret != "" {
			if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != b.cfg.WebhookSecret {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		var u tele.Update
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			b.logger.Warn("bot: bad webhook payload", "err", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		b.b.ProcessUpdate(u)
		w.WriteHeader(http.StatusOK)
	})
}

func (b *Bot) authMiddleware(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		if len(b.allowed) == 0 {
			return next(c)
		}
		chat := c.Chat()
		if chat == nil || !b.allowed[chat.ID] {
			id := int64(0)
			username := ""
			if chat != nil {
				id = chat.ID
			}
			if c.Sender() != nil {
				username = c.Sender().Username
			}
			b.logger.Warn("bot: unauthorized chat", "chat_id", id, "user", username)
			return c.Send("Доступ запрещён.")
		}
		return next(c)
	}
}
