package bot

import (
	"strings"

	tele "gopkg.in/telebot.v3"

	"visits/internal/model"
)

// onSave persists the pending parsed items behind the tapped card.
func (b *Bot) onSave(c tele.Context) error {
	key := c.Data()
	parsed, ok := b.pending.take(key)
	if !ok {
		_ = c.Respond(&tele.CallbackResponse{Text: "Карточка устарела, отправь текст заново", ShowAlert: true})
		return nil
	}

	items := make([]model.Appointment, 0, len(parsed))
	for _, p := range parsed {
		items = append(items, p.Appointment)
	}
	saved, err := b.store.CreateMany(items)
	if err != nil {
		b.logger.Error("bot: save appointments", "err", err, "n", len(items))
		_ = c.Respond(&tele.CallbackResponse{Text: "Не удалось сохранить 😕", ShowAlert: true})
		return nil
	}

	var sb strings.Builder
	sb.WriteString("✅ Сохранено:\n\n")
	sb.WriteString(b.formatList(saved))
	_ = c.Respond()
	b.mirrorToGroup(c, b.groupAddText(c, saved))
	return c.Edit(sb.String(), tele.ModeHTML)
}

// onCancel drops the pending items and closes the card.
func (b *Bot) onCancel(c tele.Context) error {
	b.pending.take(c.Data())
	_ = c.Respond()
	return c.Edit("Отменено.")
}
