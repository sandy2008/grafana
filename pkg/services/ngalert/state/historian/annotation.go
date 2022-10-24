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

func (h *AnnotationStateHistorian) RecordStates(ctx context.Context, states []state.StateTransition) {
	// Build annotations before starting goroutine, to make sure all data is copied and won't mutate underneath us.
	annotations := h.buildAnnotations(states)
	go h.recordAnnotationsSync(ctx, annotations)
}

type itemWithMetadata struct {
	annotations.Item
	dashUID       string
	parsedPanelID int64
}

func (h *AnnotationStateHistorian) buildAnnotations(states []state.StateTransition) []itemWithMetadata {
	items := make([]itemWithMetadata, 0, len(states))
	for _, state := range states {
		h.log.Debug("alert state changed creating annotation", "alertRuleUID", state.AlertRuleUID, "newState", state.Formatted(), "oldState", state.PreviousFormatted())

		labels := removePrivateLabels(state.Labels)
		annotationText := fmt.Sprintf("%s {%s} - %s", state.Rule.Title, labels.String(), state.Formatted())

		item := itemWithMetadata{
			Item: annotations.Item{
				AlertId:   state.Rule.ID,
				OrgId:     state.OrgID,
				PrevState: state.PreviousFormatted(),
				NewState:  state.Formatted(),
				Text:      annotationText,
				Epoch:     state.LastEvaluationTime.UnixNano() / int64(time.Millisecond),
			},
		}

		dashUID, ok := state.Annotations[ngmodels.DashboardUIDAnnotation]
		if ok {
			panelUID := state.Annotations[ngmodels.PanelIDAnnotation]

			panelID, err := strconv.ParseInt(panelUID, 10, 64)
			if err != nil {
				h.log.Error("error parsing panelUID for alert annotation", "panelUID", panelUID, "alertRuleUID", state.AlertRuleUID, "error", err.Error())
				continue
			}
			item.dashUID = dashUID
			item.parsedPanelID = panelID
		}
		items = append(items, item)
	}
	return items
}

func (h *AnnotationStateHistorian) recordAnnotationsSync(ctx context.Context, items []itemWithMetadata) {
	annotations := make([]annotations.Item, 0, len(items))

	for _, item := range items {
		if item.dashUID != "" {
			dashID, err := h.dashboards.getID(ctx, item.OrgId, item.dashUID)
			if err != nil {
				h.log.Error("Error getting dashboard for alert annotation", "dashboardUID", item.dashUID, "alertRuleID", item.AlertId, "error", err)
				return
			}

			item.Item.PanelId = item.parsedPanelID
			item.Item.DashboardId = dashID
		}
		annotations = append(annotations, item.Item)
	}

	if err := h.annotations.SaveMany(ctx, annotations); err != nil {
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
