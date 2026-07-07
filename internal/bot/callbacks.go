package bot

import (
	"fmt"
	"strconv"
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
	return c.Edit(sb.String(), tele.ModeHTML)
}

// onCancel drops the pending items and closes the card.
func (b *Bot) onCancel(c tele.Context) error {
	b.pending.take(c.Data())
	_ = c.Respond()
	return c.Edit("Отменено.")
}

// onReschedule arms the reschedule flow: the sender's next text message is
// read as the new datetime for this appointment.
func (b *Bot) onReschedule(c tele.Context) error {
	id, err := strconv.ParseInt(c.Data(), 10, 64)
	if err != nil {
		_ = c.Respond()
		return nil
	}
	a, err := b.store.Get(id)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "Визит не найден", ShowAlert: true})
		return nil
	}
	b.awaiting.set(senderID(c), id, b.now())
	_ = c.Respond()
	return c.Send(fmt.Sprintf("Перенос «%s». Напиши новую дату и время — например: в пятницу 17:00", a.Title))
}

// onCancelAppt soft-cancels an appointment; it drops out of the ICS feed on
// HA's next poll.
func (b *Bot) onCancelAppt(c tele.Context) error {
	id, err := strconv.ParseInt(c.Data(), 10, 64)
	if err != nil {
		_ = c.Respond()
		return nil
	}
	a, err := b.store.Get(id)
	if err != nil {
		_ = c.Respond(&tele.CallbackResponse{Text: "Визит не найден", ShowAlert: true})
		return nil
	}
	if err := b.store.SetStatus(id, model.StatusCancelled); err != nil {
		b.logger.Error("bot: cancel appointment", "err", err, "id", id)
		_ = c.Respond(&tele.CallbackResponse{Text: "Не удалось отменить 😕", ShowAlert: true})
		return nil
	}
	_ = c.Respond()
	return c.Edit("✗ Отменено: "+b.formatAppt(a), tele.ModeHTML)
}
