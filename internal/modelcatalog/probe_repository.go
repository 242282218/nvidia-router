package modelcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (r *Repository) applyProbe(ctx context.Context, id int64, update probeCapabilityUpdate, now time.Time) error {
	if update.SupportsReasoning == nil && update.ReasoningStatus == nil && update.ReasoningWireFormat == nil && update.ToolsStatus == nil {
		return nil
	}
	model, err := r.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("load model before capability probe update: %w", err)
	}
	sets := make([]string, 0, 5)
	args := make([]any, 0, 6)
	if update.SupportsReasoning != nil {
		sets = append(sets, "supports_reasoning = ?")
		args = append(args, boolInt(*update.SupportsReasoning))
	}
	if update.ReasoningStatus != nil {
		if !validProbeReasoningStatus(*update.ReasoningStatus) {
			return fmt.Errorf("invalid probe reasoning status %q", *update.ReasoningStatus)
		}
		sets = append(sets, "reasoning_status = ?")
		args = append(args, *update.ReasoningStatus)
	}
	if update.ReasoningWireFormat != nil {
		sets = append(sets, "reasoning_wire_format = ?")
		args = append(args, *update.ReasoningWireFormat)
	}
	if update.ToolsStatus != nil {
		if *update.ToolsStatus != ToolsStatusSupported && *update.ToolsStatus != ToolsStatusUnsupported {
			return fmt.Errorf("invalid probe tools status %q", *update.ToolsStatus)
		}
		sets = append(sets, "tools_status = ?", "tools_verified_at = ?", "supports_tools = ?")
		args = append(args, *update.ToolsStatus, optionalTimestamp(update.ToolsVerifiedAt), boolInt(*update.ToolsStatus == ToolsStatusSupported))
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, formatRevisionTime(now, model.updatedAt))
	args = append(args, id)
	result, err := r.db.ExecContext(ctx, "UPDATE models SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return fmt.Errorf("apply capability probe update: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("count capability probe update: %w", err)
	} else if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func validProbeReasoningStatus(status string) bool {
	switch status {
	case ReasoningStatusVisible, ReasoningStatusHidden, ReasoningStatusUnsupported:
		return true
	default:
		return false
	}
}
