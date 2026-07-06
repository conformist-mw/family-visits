package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"visits/internal/model"
	"visits/internal/parse"
)

const startText = `Привет! Пиши будущие визиты как обычно — датой, временем и «что + кто». Можно несколько за раз:

8.07 11:30 Педикюр Олежа
16.07 11:45 Ортодонт я

Я разберу и переспрошу перед сохранением.

Команды:
/week — что на ближайшую неделю
/list — все будущие визиты`

func (b *Bot) cmdStart(c tele.Context) error {
	return c.Send(startText)
}

func (b *Bot) cmdList(c tele.Context) error {
	now := b.now()
	items, err := b.store.Upcoming(now.Format(model.LocalDatetime), 50)
	if err != nil {
		b.logger.Error("bot: upcoming query", "err", err)
		return c.Send("Не смог достать список 😕")
	}
	if len(items) == 0 {
		return c.Send("Будущих визитов нет.")
	}
	return c.Send("📋 Все будущие визиты:\n\n"+b.formatList(items), tele.ModeHTML)
}

func (b *Bot) cmdWeek(c tele.Context) error {
	return c.Send(b.weekDigest(), tele.ModeHTML)
}

// onText is the capture path: parse free text, show a confirmation card.
func (b *Bot) onText(c tele.Context) error {
	text := strings.TrimSpace(c.Text())
	if text == "" || strings.HasPrefix(text, "/") {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := b.now()
	parsed, err := b.parser.Parse(ctx, text, now)
	if err != nil {
		b.logger.Error("bot: parse", "err", err)
		return c.Send("Не смог разобрать текст 😕 Попробуй ещё раз.")
	}
	if len(parsed) == 0 {
		return c.Send("Не нашёл визитов. Пример: 8.07 11:30 Педикюр Олежа")
	}

	// Unspecified (or self-referential) "who" defaults to the message sender —
	// the parser can't know who sent the message, so we resolve it here.
	resolvePerson(parsed, senderName(c))

	key := b.pending.put(parsed, now)
	return c.Send(b.confirmText(parsed), b.confirmMarkup(key, len(parsed)), tele.ModeHTML)
}

func (b *Bot) confirmText(parsed []parse.Parsed) string {
	var sb strings.Builder
	sb.WriteString("Нашёл, сохранить?\n\n")
	for _, p := range parsed {
		sb.WriteString(b.formatParsed(p))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (b *Bot) confirmMarkup(key string, n int) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	m.Inline(m.Row(
		m.Data(fmt.Sprintf("✅ Сохранить (%d)", n), "appt_save", key),
		m.Data("✗ Отмена", "appt_cancel", key),
	))
	return m
}

// ── formatting ───────────────────────────────────────────────────────────────

var monthsRU = [...]string{
	"", "янв", "фев", "мар", "апр", "мая", "июн",
	"июл", "авг", "сен", "окт", "ноя", "дек",
}

var weekdaysShortRU = [7]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}

func (b *Bot) formatParsed(p parse.Parsed) string {
	line := b.formatAppt(p.Appointment)
	if p.Confidence == "low" {
		line += " ⚠️"
	}
	return line
}

func (b *Bot) formatAppt(a model.Appointment) string {
	t, err := a.Start(b.cfg.Loc)
	when := a.StartsAt
	if err == nil {
		when = fmt.Sprintf("%s %d %s, %02d:%02d",
			weekdaysShortRU[int(t.Weekday())], t.Day(), monthsRU[int(t.Month())], t.Hour(), t.Minute())
	}
	who := ""
	if a.Person != "" {
		who = " · " + a.Person
	}
	return fmt.Sprintf("📌 <b>%s</b> — %s%s", a.Title, when, who)
}

func (b *Bot) formatList(items []model.Appointment) string {
	lines := make([]string, 0, len(items))
	for _, a := range items {
		lines = append(lines, b.formatAppt(a))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) now() time.Time { return time.Now().In(b.cfg.Loc) }

// senderName is the best display name for the message author, used as the
// default "who".
func senderName(c tele.Context) string {
	u := c.Sender()
	if u == nil {
		return ""
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	return u.Username
}

// resolvePerson fills an empty or self-referential person with self (the
// sender). Named people ("Олежа", "обоє") are left untouched.
func resolvePerson(parsed []parse.Parsed, self string) {
	if self == "" {
		return
	}
	for i := range parsed {
		switch strings.ToLower(strings.TrimSpace(parsed[i].Appointment.Person)) {
		case "", "я", "мене", "мне", "себе", "собі":
			parsed[i].Appointment.Person = self
		}
	}
}
