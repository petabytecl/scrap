package templates

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/a-h/templ"
)

type DashboardData struct {
	ActiveView       string
	GeneratedAt      string
	Nav              []NavItem
	Summary          SummaryData
	Capacity         CapacityData
	Operations       []OperationData
	RepairQueueCount int
	Signals          []ReadinessSignal
	Errors           []string
}

type NavItem struct {
	ID    string
	Label string
	Group string
	Href  string
}

type SummaryData struct {
	ShardCount            uint32
	StorageMemberCount    uint32
	LocalBytesUsed        uint64
	LocalBytesCapacity    uint64
	DegradedDocumentCount uint32
	PendingOperationCount uint32
}

type CapacityData struct {
	ProfileID              string
	UsableBytesRemaining   uint64
	EstimatedBytesPerDay   uint64
	RunwayDays             uint32
	Warnings               []WarningData
	HasMeasuredIngressRate bool
}

type WarningData struct {
	Code    string
	Message string
}

type OperationData struct {
	ID        string
	Type      string
	State     string
	Requested string
	Completed uint64
	Total     uint64
	Message   string
}

type ReadinessSignal struct {
	Name   string
	State  string
	Detail string
}

func navClass(data DashboardData, item NavItem) string {
	if data.ActiveView == item.ID {
		return "nav-item active"
	}
	return "nav-item"
}

func groupedNav(items []NavItem) []navGroup {
	groups := make([]navGroup, 0, len(items))
	index := make(map[string]int, len(items))
	for _, item := range items {
		if existing, ok := index[item.Group]; ok {
			groups[existing].Items = append(groups[existing].Items, item)
			continue
		}
		index[item.Group] = len(groups)
		groups = append(groups, navGroup{Name: item.Group, Items: []NavItem{item}})
	}
	return groups
}

type navGroup struct {
	Name  string
	Items []NavItem
}

func pageTitle(activeView string) string {
	switch activeView {
	case "capacity":
		return "Capacity"
	case "members":
		return "Members"
	case "raft":
		return "Raft consensus"
	case "clients":
		return "Service mesh"
	case "backend":
		return "Backend"
	case "openbao":
		return "OpenBao"
	case "operations":
		return "Operations"
	case "repair":
		return "Repair"
	default:
		return "Overview"
	}
}

func bytesText(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}

func numberText(value uint64) string {
	return NumberText(value)
}

func countText(value int) string {
	return CountText(value)
}

func NumberText(value uint64) string {
	raw := strconv.FormatUint(value, 10)
	return commaText(raw)
}

func CountText(value int) string {
	raw := strconv.Itoa(value)
	return commaText(raw)
}

func commaText(raw string) string {
	var builder strings.Builder
	for i, char := range raw {
		if i > 0 && (len(raw)-i)%3 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func percentText(used, capacity uint64) string {
	if capacity == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(used)/float64(capacity)*100)
}

func percentValue(used, capacity uint64) string {
	if capacity == 0 {
		return "0"
	}
	value := math.Min(float64(used)/float64(capacity)*100, 100)
	return fmt.Sprintf("%.1f", value)
}

func progressWidth(completed, total uint64) string {
	if total == 0 {
		return "0"
	}
	value := math.Min(float64(completed)/float64(total)*100, 100)
	return fmt.Sprintf("%.1f", value)
}

func stateClass(state string) string {
	switch state {
	case "OPERATION_STATE_RUNNING":
		return "status running"
	case "OPERATION_STATE_QUEUED":
		return "status queued"
	case "OPERATION_STATE_SUCCEEDED":
		return "status succeeded"
	case "OPERATION_STATE_FAILED":
		return "status failed"
	default:
		return "status"
	}
}

func shortState(state string) string {
	state = strings.TrimPrefix(state, "OPERATION_STATE_")
	state = strings.ToLower(strings.ReplaceAll(state, "_", " "))
	if state == "" {
		return "unknown"
	}
	return state
}

func signalClass(state string) string {
	switch state {
	case "healthy":
		return "signal healthy"
	case "warning", "advisory":
		return "signal warning"
	case "external":
		return "signal external"
	default:
		return "signal unknown"
	}
}

func safeURL(value string) templ.SafeURL {
	return templ.SafeURL(value)
}

func widthStyle(value string) templ.SafeCSS {
	return templ.SafeCSS("width: " + value + "%;")
}
