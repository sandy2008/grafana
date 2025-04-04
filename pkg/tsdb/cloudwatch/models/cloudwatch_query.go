package models

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/tsdb/cloudwatch/cwlog"
)

type (
	MetricEditorMode uint32
	MetricQueryType  uint32
	GMDApiMode       uint32
)

const (
	MetricEditorModeBuilder MetricEditorMode = iota
	MetricEditorModeRaw
)

const (
	MetricQueryTypeSearch MetricQueryType = iota
	MetricQueryTypeQuery
)

const (
	GMDApiModeMetricStat GMDApiMode = iota
	GMDApiModeInferredSearchExpression
	GMDApiModeMathExpression
	GMDApiModeSQLExpression
)

type CloudWatchQuery struct {
	RefId             string
	Region            string
	Id                string
	Namespace         string
	MetricName        string
	Statistic         string
	Expression        string
	SqlExpression     string
	ReturnData        bool
	Dimensions        map[string][]string
	Period            int
	Alias             string
	Label             string
	MatchExact        bool
	UsedExpression    string
	TimezoneUTCOffset string
	MetricQueryType   MetricQueryType
	MetricEditorMode  MetricEditorMode
}

func (q *CloudWatchQuery) GetGMDAPIMode() GMDApiMode {
	if q.MetricQueryType == MetricQueryTypeSearch && q.MetricEditorMode == MetricEditorModeBuilder {
		if q.IsInferredSearchExpression() {
			return GMDApiModeInferredSearchExpression
		}
		return GMDApiModeMetricStat
	} else if q.MetricQueryType == MetricQueryTypeSearch && q.MetricEditorMode == MetricEditorModeRaw {
		return GMDApiModeMathExpression
	} else if q.MetricQueryType == MetricQueryTypeQuery {
		return GMDApiModeSQLExpression
	}

	cwlog.Warn("could not resolve CloudWatch metric query type. Falling back to metric stat.", "query", q)
	return GMDApiModeMetricStat
}

func (q *CloudWatchQuery) IsMathExpression() bool {
	return q.MetricQueryType == MetricQueryTypeSearch && q.MetricEditorMode == MetricEditorModeRaw && !q.IsUserDefinedSearchExpression()
}

func (q *CloudWatchQuery) isSearchExpression() bool {
	return q.MetricQueryType == MetricQueryTypeSearch && (q.IsUserDefinedSearchExpression() || q.IsInferredSearchExpression())
}

func (q *CloudWatchQuery) IsUserDefinedSearchExpression() bool {
	return q.MetricQueryType == MetricQueryTypeSearch && q.MetricEditorMode == MetricEditorModeRaw && strings.Contains(q.Expression, "SEARCH(")
}

func (q *CloudWatchQuery) IsInferredSearchExpression() bool {
	if q.MetricQueryType != MetricQueryTypeSearch || q.MetricEditorMode != MetricEditorModeBuilder {
		return false
	}

	if len(q.Dimensions) == 0 {
		return !q.MatchExact
	}
	if !q.MatchExact {
		return true
	}

	for _, values := range q.Dimensions {
		if len(values) > 1 {
			return true
		}
		for _, v := range values {
			if v == "*" {
				return true
			}
		}
	}
	return false
}

func (q *CloudWatchQuery) IsMultiValuedDimensionExpression() bool {
	if q.MetricQueryType != MetricQueryTypeSearch || q.MetricEditorMode != MetricEditorModeBuilder {
		return false
	}

	for _, values := range q.Dimensions {
		for _, v := range values {
			if v == "*" {
				return false
			}
		}

		if len(values) > 1 {
			return true
		}
	}

	return false
}

func (q *CloudWatchQuery) BuildDeepLink(startTime time.Time, endTime time.Time, dynamicLabelEnabled bool) (string, error) {
	if q.IsMathExpression() || q.MetricQueryType == MetricQueryTypeQuery {
		return "", nil
	}

	link := &cloudWatchLink{
		Title:   q.RefId,
		View:    "timeSeries",
		Stacked: false,
		Region:  q.Region,
		Start:   startTime.UTC().Format(time.RFC3339),
		End:     endTime.UTC().Format(time.RFC3339),
	}

	if q.isSearchExpression() {
		metricExpressions := &metricExpression{Expression: q.UsedExpression}
		if dynamicLabelEnabled {
			metricExpressions.Label = q.Label
		}
		link.Metrics = []interface{}{metricExpressions}
	} else {
		metricStat := []interface{}{q.Namespace, q.MetricName}
		for dimensionKey, dimensionValues := range q.Dimensions {
			metricStat = append(metricStat, dimensionKey, dimensionValues[0])
		}
		metricStatMeta := &metricStatMeta{
			Stat:   q.Statistic,
			Period: q.Period,
		}
		if dynamicLabelEnabled {
			metricStatMeta.Label = q.Label
		}
		metricStat = append(metricStat, metricStatMeta)
		link.Metrics = []interface{}{metricStat}
	}

	linkProps, err := json.Marshal(link)
	if err != nil {
		return "", fmt.Errorf("could not marshal link: %w", err)
	}

	url, err := url.Parse(fmt.Sprintf(`https://%s.console.aws.amazon.com/cloudwatch/deeplink.js`, q.Region))
	if err != nil {
		return "", fmt.Errorf("unable to parse CloudWatch console deep link")
	}

	fragment := url.Query()
	fragment.Set("graph", string(linkProps))

	query := url.Query()
	query.Set("region", q.Region)
	url.RawQuery = query.Encode()

	return fmt.Sprintf(`%s#metricsV2:%s`, url.String(), fragment.Encode()), nil
}
