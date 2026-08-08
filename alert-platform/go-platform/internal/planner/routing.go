package planner

import (
	"sort"
	"strconv"
	"strings"
)

type Subscription struct {
	Username          string
	Subsidiary        *string
	ServiceID         *string
	PriorityThreshold *string
}

func ResolveRecipients(
	subsidiaries map[string]struct{},
	serviceIDs map[string]struct{},
	priority *string,
	subscriptions []Subscription,
) []string {
	problemRank, hasProblemRank := priorityRank(priority)
	recipients := make(map[string]struct{})
	for _, subscription := range subscriptions {
		if subscription.Subsidiary != nil {
			if _, ok := subsidiaries[*subscription.Subsidiary]; !ok {
				continue
			}
		}
		if subscription.ServiceID != nil {
			if _, ok := serviceIDs[*subscription.ServiceID]; !ok {
				continue
			}
		}
		if thresholdRank, ok := priorityRank(subscription.PriorityThreshold); ok && hasProblemRank && problemRank > thresholdRank {
			continue
		}
		recipients[subscription.Username] = struct{}{}
	}
	result := make([]string, 0, len(recipients))
	for recipient := range recipients {
		result = append(result, recipient)
	}
	sort.Strings(result)
	return result
}

func priorityRank(priority *string) (int, bool) {
	if priority == nil || !strings.HasPrefix(*priority, "P") {
		return 0, false
	}
	rank, err := strconv.Atoi(strings.TrimPrefix(*priority, "P"))
	return rank, err == nil
}
