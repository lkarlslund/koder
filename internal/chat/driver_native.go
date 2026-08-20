package chat

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

// NativeTurnDriver adapts Koder's existing provider/tool loop to the generic
// backend turn boundary. Its behavior intentionally remains identical to the
// pre-driver implementation.
type NativeTurnDriver struct {
	Model ModelRuntime
}

func (d NativeTurnDriver) RunTurn(ctx context.Context, rt *Chat, turn DriverTurn, out chan<- domain.Event) error {
	if d.Model == nil {
		return fmt.Errorf("model service is not configured")
	}
	var (
		turnInstructions []provider.InstructionBlock
		err              error
	)
	switch domain.DeliveryForQueuedInput(turn.Input) {
	case domain.QueuedInputDeliveryContinue:
		turnInstructions, err = d.Model.PrepareContinueTurn(ctx, rt, turn.Note, out)
	default:
		turnInstructions, err = d.Model.PreparePromptTurn(
			ctx,
			rt,
			turn.Input,
			turn.Input.Text,
			queuedAttachmentDrafts(turn.Input.Attachments),
			queuedReferenceDrafts(turn.Input.References),
			turn.Note,
			out,
		)
	}
	if err != nil {
		return err
	}
	rt.continueTurnLoop(ctx, turnInstructions, turn.EphemeralInstructions, out)
	return nil
}
