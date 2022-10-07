package historian

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/services/annotations"
	"github.com/grafana/grafana/pkg/services/dashboards"
	ngmodels "github.com/grafana/grafana/pkg/services/ngalert/models"
	"github.com/grafana/grafana/pkg/services/ngalert/state"
)

// AnnotationStateHistorian is an implementation of state.Historian that uses Grafana Annotations as the backing datastore.
type AnnotationStateHistorian struct {
	annotations annotations.Repository
	dashboards  *dashboardResolver
	log         log.Logger
}

func NewAnnotationHistorian(annotations annotations.Repository, dashboards dashboards.DashboardService, log log.Logger) *AnnotationStateHistorian {
	return &AnnotationStateHistorian{
		annotations: annotations,
		dashboards:  newDashboardResolver(dashboards, log, defaultDashboardCacheExpiry),
		log:         log,
	}
}

func (h *AnnotationStateHistorian) RecordStates(ctx context.Context, states []state.ContextualState) {
	go h.recordStatesSync(ctx, states)
}

func (h *AnnotationStateHistorian) recordStatesSync(ctx context.Context, states []state.ContextualState) {
	items := make([]annotations.Item, 0, len(states))
	for _, state := range states {
		h.log.Debug("alert state changed creating annotation", "alertRuleUID", state.AlertRuleUID, "newState", state.Formatted(), "oldState", state.PreviousFormatted())

		labels := removePrivateLabels(state.Labels)
		annotationText := fmt.Sprintf("%s {%s} - %s", state.RuleTitle, labels.String(), state.Formatted())

		item := annotations.Item{
			AlertId:   state.RuleID,
			OrgId:     state.OrgID,
			PrevState: state.PreviousFormatted(),
			NewState:  state.Formatted(),
			Text:      annotationText,
			Epoch:     state.LastEvaluationTime.UnixNano() / int64(time.Millisecond),
		}

		dashUid, ok := state.Annotations[ngmodels.DashboardUIDAnnotation]
		if ok {
			panelUid := state.Annotations[ngmodels.PanelIDAnnotation]

			panelId, err := strconv.ParseInt(panelUid, 10, 64)
			if err != nil {
				h.log.Error("error parsing panelUID for alert annotation", "panelUID", panelUid, "alertRuleUID", state.AlertRuleUID, "error", err.Error())
				return
			}

			dashID, err := h.dashboards.getID(ctx, state.OrgID, dashUid)
			if err != nil {
				h.log.Error("error getting dashboard for alert annotation", "dashboardUID", dashUid, "alertRuleUID", state.AlertRuleUID, "error", err.Error())
				return
			}

			item.PanelId = panelId
			item.DashboardId = dashID
		}
		items = append(items, item)
	}

	if err := h.annotations.SaveMany(ctx, items); err != nil {
		affectedIDs := make([]int64, 0, len(items))
		for _, i := range items {
			affectedIDs = append(affectedIDs, i.AlertId)
		}
		h.log.Error("error saving alert annotation batch", "alertRuleIDs", affectedIDs, "err", err.Error())
		return
	}
}

func removePrivateLabels(labels data.Labels) data.Labels {
	result := make(data.Labels)
	for k, v := range labels {
		if !strings.HasPrefix(k, "__") && !strings.HasSuffix(k, "__") {
			result[k] = v
		}
	}
	return result
}
