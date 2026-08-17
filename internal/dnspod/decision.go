package dnspod

import (
	"fmt"
	"strings"

	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type TrafficPlan struct {
	Action  string
	Current *domain.DNSRecord
	Type    string
	Value   string
}

func PlanTraffic(records []domain.DNSRecord, desiredType, desiredValue string) (TrafficPlan, error) {
	desiredType = strings.ToUpper(strings.TrimSpace(desiredType))
	if desiredType != "A" && desiredType != "CNAME" {
		return TrafficPlan{}, fmt.Errorf("traffic record type must be A or CNAME")
	}
	traffic := make([]domain.DNSRecord, 0, len(records))
	for _, record := range records {
		if record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME" {
			traffic = append(traffic, record)
		}
	}
	if len(traffic) > 1 {
		return TrafficPlan{}, fmt.Errorf("multiple apex A/AAAA/CNAME records are ambiguous")
	}
	plan := TrafficPlan{Action: "create", Type: desiredType, Value: desiredValue}
	if len(traffic) == 0 {
		return plan, nil
	}
	current := traffic[0]
	plan.Current = &current
	if current.Type == desiredType && domain.EqualTarget(current.Content, desiredValue) {
		plan.Action = "unchanged"
	} else {
		plan.Action = "modify"
	}
	return plan, nil
}
