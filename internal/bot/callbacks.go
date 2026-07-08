package bot

import (
	"strings"

	tele "gopkg.in/telebot.v3"

	"visits/internal/model"
)

// onSave persists the pending parsed items behind the tapped card.
func (b *Bot) onSave(c tele.Context) error {
	key := c.Data()
	entry, ok := b.pending.take(key)
	if !ok {
		_ = c.Respond(&tele.CallbackResponse{Text: "Картка застаріла, надішли текст ще раз", ShowAlert: true})
		return nil
	}

	items := make([]model.Appointment, 0, len(entry.parsed))
	for _, p := range entry.parsed {
		items = append(items, p.Appointment)
	}
	saved, err := b.store.CreateMany(items)
	if err != nil {
		b.logger.Error("bot: save appointments", "err", err, "n", len(items))
		_ = c.Respond(&tele.CallbackResponse{Text: "Не вдалося зберегти 😕", ShowAlert: true})
		return nil
	}

	var sb strings.Builder
	sb.WriteString("✅ Збережено:\n\n")
	sb.WriteString(b.formatList(saved))
	_ = c.Respond()
	b.mirrorToGroup(c, b.groupAddText(c, saved))
	return c.Edit(sb.String(), tele.ModeHTML)
}

// onUpdate applies a same-time capture onto the existing visit (title/person)
// instead of creating a second entry.
func (b *Bot) onUpdate(c tele.Context) error {
	entry, ok := b.pending.take(c.Data())
	if !ok {
		_ = c.Respond(&tele.CallbackResponse{Text: "Картка застаріла, надішли текст ще раз", ShowAlert: true})
		return nil
	}
	if entry.updateID == 0 || len(entry.parsed) == 0 {
		_ = c.Respond(&tele.CallbackResponse{Text: "Нема чого оновлювати", ShowAlert: true})
		return nil
	}
	n := entry.parsed[0].Appointment
	if err := b.store.UpdateDetails(entry.updateID, n.Title, n.Person); err != nil {
		b.logger.Error("bot: update details", "err", err, "id", entry.updateID)
		_ = c.Respond(&tele.CallbackResponse{Text: "Не вдалося оновити 😕", ShowAlert: true})
		return nil
	}
	a, err := b.store.Get(entry.updateID)
	if err != nil {
		_ = c.Respond()
		return c.Edit("Оновив, але не зміг показати 🤔")
	}
	_ = c.Respond()
	b.mirrorToGroup(c, b.groupChangeText(c, a, "оновлено"))
	return c.Edit("✅ Оновлено:\n"+b.formatAppt(a), tele.ModeHTML)
}

// onCancel drops the pending items and closes the card.
func (b *Bot) onCancel(c tele.Context) error {
	b.pending.take(c.Data())
	_ = c.Respond()
	return c.Edit("Скасовано.")
}
