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

const startText = `Привет! Я веду семейные визиты (маникюр, ортодонт, врачи…).

Добавить — командой /visit и текстом, можно несколько строк:
/visit 8.07 11:30 Педикюр Олежа
/visit завтра 15:00 ортодонт

Если «кто» не указан — подставлю того, кто написал.

Команды:
/visit — добавить визит
/week — что на ближайшую неделю
/list — визиты по неделям (перенос, правка, отмена)
/help — эта справка

(В личке со мной можно писать визиты и без /visit.)`

func (b *Bot) cmdStart(c tele.Context) error {
	return c.Send(startText)
}

// cmdVisit is the explicit capture trigger — the only way to add a visit in a
// group, where free text is other people's chatter.
func (b *Bot) cmdVisit(c tele.Context) error {
	text := commandPayload(c.Text())
	if text == "" {
		return c.Send("Напиши визит после команды, например:\n/visit завтра 15:00 педикюр")
	}
	return b.captureText(c, text, b.now())
}

func (b *Bot) cmdList(c tele.Context) error {
	text, markup, empty, err := b.listView(0)
	if err != nil {
		b.logger.Error("bot: list view", "err", err)
		return c.Send("Не смог достать список 😕")
	}
	if empty {
		return c.Send("Будущих визитов нет.")
	}
	return c.Send(text, markup, tele.ModeHTML)
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

	// If this user just tapped a field-edit button, their next message is the new
	// value for that visit (time/title/who) — handled in any chat type.
	if apptID, field, ok := b.awaiting.take(senderID(c), now); ok {
		return b.applyEdit(c, apptID, field, text, now)
	}

	// In groups the bot must not run every message through Gemini — capture is
	// explicit via /add. Free text is a visit only in a private chat.
	if !isPrivate(c) {
		return nil
	}
	return b.captureText(c, text, now)
}

// captureText parses free text into appointments and shows a confirmation card.
func (b *Bot) captureText(c tele.Context, text string, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parsed, err := b.parser.Parse(ctx, text, now)
	if err != nil {
		b.logger.Error("bot: parse", "err", err)
		return c.Send("Не смог разобрать текст 😕 Попробуй ещё раз.")
	}
	if len(parsed) == 0 {
		return c.Send("Не нашёл визитов. Пример: /visit 8.07 11:30 Педикюр Олежа")
	}

	// Unspecified (or self-referential) "who" defaults to the message sender —
	// the parser can't know who sent the message, so we resolve it here.
	resolvePerson(parsed, senderName(c))

	key := b.pending.put(parsed, now)
	return c.Send(b.confirmText(parsed), b.confirmMarkup(key, len(parsed)), tele.ModeHTML)
}

func isPrivate(c tele.Context) bool {
	return c.Chat() != nil && c.Chat().Type == tele.ChatPrivate
}

// commandPayload returns everything after the leading /command token, handling
// "/add@bot rest" and multi-line "/add\nrest".
func commandPayload(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return text
	}
	i := strings.IndexAny(text, " \n\t")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(text[i+1:])
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

// whenLabel renders an appointment's start as "пн 8 июл, 10:30" (falls back to
// the raw stored value if it can't be parsed).
func (b *Bot) whenLabel(a model.Appointment) string {
	t, err := a.Start(b.cfg.Loc)
	if err != nil {
		return a.StartsAt
	}
	return fmt.Sprintf("%s %d %s, %02d:%02d",
		weekdaysShortRU[int(t.Weekday())], t.Day(), monthsRU[int(t.Month())], t.Hour(), t.Minute())
}

func (b *Bot) formatAppt(a model.Appointment) string {
	who := ""
	if a.Person != "" {
		who = " · " + a.Person
	}
	return fmt.Sprintf("📌 <b>%s</b> — %s%s", a.Title, b.whenLabel(a), who)
}

func (b *Bot) formatList(items []model.Appointment) string {
	lines := make([]string, 0, len(items))
	for _, a := range items {
		lines = append(lines, b.formatAppt(a))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) now() time.Time { return time.Now().In(b.cfg.Loc) }

// ── group mirror ─────────────────────────────────────────────────────────────

// mirrorToGroup echoes a private-chat add/update/cancel into the family group,
// so the group stays the shared source of truth even when someone captures a
// visit in a 1:1 chat with the bot. It's a no-op in the group itself (the
// action's confirmation card is already visible there, so mirroring would
// double-post) and when no notify chat is configured.
func (b *Bot) mirrorToGroup(c tele.Context, text string) {
	if !isPrivate(c) || b.cfg.NotifyChat == 0 {
		return
	}
	if _, err := b.b.Send(tele.ChatID(b.cfg.NotifyChat), text, tele.ModeHTML); err != nil {
		b.logger.Error("bot: mirror to group", "err", err)
	}
}

func (b *Bot) groupAddText(c tele.Context, items []model.Appointment) string {
	head := "🆕 Новый визит"
	if len(items) > 1 {
		head = "🆕 Новые визиты"
	}
	return head + byLine(c) + ":\n\n" + b.formatList(items)
}

func (b *Bot) groupChangeText(c tele.Context, a model.Appointment, verb string) string {
	return "🔄 Визит " + verb + byLine(c) + ":\n" + b.formatAppt(a)
}

func (b *Bot) groupCancelText(c tele.Context, a model.Appointment) string {
	return "✗ Визит отменён" + byLine(c) + ":\n" + b.formatAppt(a)
}

// byLine attributes a group notification to whoever made the change.
func byLine(c tele.Context) string {
	if who := senderName(c); who != "" {
		return " (" + who + ")"
	}
	return ""
}

// applyEdit routes a follow-up text message to the field the user chose to edit.
func (b *Bot) applyEdit(c tele.Context, apptID int64, field, text string, now time.Time) error {
	switch field {
	case "title":
		return b.applyFieldEdit(c, apptID, field, text, b.store.UpdateTitle)
	case "who":
		return b.applyFieldEdit(c, apptID, field, text, b.store.UpdatePerson)
	default: // time
		return b.applyReschedule(c, apptID, text, now)
	}
}

// applyReschedule parses a datetime from text and moves the appointment.
func (b *Bot) applyReschedule(c tele.Context, apptID int64, text string, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	when, _, err := b.parser.ParseWhen(ctx, text, now)
	if err != nil {
		b.logger.Error("bot: parse when", "err", err)
		b.awaiting.set(senderID(c), apptID, "time", now) // keep it, let the user retry
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
	b.mirrorToGroup(c, b.groupChangeText(c, a, "перенесён"))
	return c.Send("✅ Перенесено:\n"+b.formatAppt(a), tele.ModeHTML)
}

// applyFieldEdit writes a free-text field (title/person) and echoes the result.
func (b *Bot) applyFieldEdit(c tele.Context, apptID int64, field, value string, update func(int64, string) error) error {
	if err := update(apptID, value); err != nil {
		b.logger.Error("bot: edit field", "err", err, "id", apptID, "field", field)
		return c.Send("Не удалось изменить 😕")
	}
	a, err := b.store.Get(apptID)
	if err != nil {
		return c.Send("Изменил, но не смог показать 🤔")
	}
	b.mirrorToGroup(c, b.groupChangeText(c, a, "изменён"))
	return c.Send("✅ Изменено:\n"+b.formatAppt(a), tele.ModeHTML)
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
