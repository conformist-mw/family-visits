package bot

import (
	"context"
	"fmt"
	"strconv"
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
	if err := c.Send("📋 Все будущие визиты:"); err != nil {
		return err
	}
	// One message per visit so each carries its own reschedule/cancel buttons.
	for _, a := range items {
		if err := c.Send(b.formatAppt(a), b.apptMarkup(a.ID), tele.ModeHTML); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) apptMarkup(id int64) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	ids := strconv.FormatInt(id, 10)
	m.Inline(m.Row(
		m.Data("→ Перенести", "appt_resched", ids),
		m.Data("✗ Отменить", "appt_del", ids),
	))
	return m
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
	now := b.now()

	// If this user just tapped "Перенести", their next message is the new time
	// for that visit — not a new appointment.
	if apptID, ok := b.awaiting.take(senderID(c), now); ok {
		return b.applyReschedule(c, apptID, text, now)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

// applyReschedule parses a datetime from text and moves the appointment.
func (b *Bot) applyReschedule(c tele.Context, apptID int64, text string, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	when, _, err := b.parser.ParseWhen(ctx, text, now)
	if err != nil {
		b.logger.Error("bot: parse when", "err", err)
		b.awaiting.set(senderID(c), apptID, now) // keep it, let the user retry
		return c.Send("Не понял дату. Напиши, например: в пятницу 17:00")
	}
	if err := b.store.Reschedule(apptID, when.Format(model.LocalDatetime)); err != nil {
		b.logger.Error("bot: reschedule", "err", err, "id", apptID)
		return c.Send("Не удалось перенести 😕")
	}
	a, err := b.store.Get(apptID)
	if err != nil {
		return c.Send("Перенёс, но не смог показать 🤔")
	}
	return c.Send("✅ Перенесено:\n"+b.formatAppt(a), tele.ModeHTML)
}

// senderID is the message author's Telegram id (0 if unknown).
func senderID(c tele.Context) int64 {
	if u := c.Sender(); u != nil {
		return u.ID
	}
	return 0
}

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
