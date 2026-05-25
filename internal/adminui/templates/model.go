package templates

import (
	"fmt"
	"math"
	"net/url"
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
	MemberDetail     MemberDetailData
	MemberTabs       []MemberTab
	ClientDetail     ClientDetailData
	ClientTabs       []ClientTab
	Operations       []OperationData
	OperationFilter  string
	OperationFilters []OperationFilter
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

const (
	MemberTabOverview   = "overview"
	MemberTabKubernetes = "kubernetes"
	MemberTabRaft       = "raft"
	MemberTabStorage    = "storage"
	MemberTabShards     = "shards"
	MemberTabEvents     = "events"
)

type MemberDetailData struct {
	ID            string
	CellID        string
	State         string
	Cordoned      bool
	BytesUsed     uint64
	BytesCapacity uint64
	LastSeen      string
	Tab           string
	Tabs          []MemberTab
	Eviction      EvictionSafetyData
}

type MemberTab struct {
	ID    string
	Label string
}

type EvictionSafetyData struct {
	Known       bool
	SafeToEvict bool
	Error       string
	Warnings    []WarningData
}

const (
	ClientTabOverview     = "overview"
	ClientTabIdentity     = "identity"
	ClientTabCapabilities = "capabilities"
	ClientTabTraffic      = "traffic"
	ClientTabEvents       = "events"
)

type ClientDetailData struct {
	ID   string
	Tab  string
	Tabs []ClientTab
}

type ClientTab struct {
	ID    string
	Label string
}

type OperationData struct {
	ID          string
	Type        string
	State       string
	RequestedBy string
	Requested   string
	Started     string
	Finished    string
	DryRun      bool
	Completed   uint64
	Total       uint64
	Message     string
}

type OperationFilter struct {
	ID    string
	Label string
	Href  string
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

func MemberTabs() []MemberTab {
	return []MemberTab{
		{ID: MemberTabOverview, Label: "Overview"},
		{ID: MemberTabKubernetes, Label: "Kubernetes"},
		{ID: MemberTabRaft, Label: "Raft"},
		{ID: MemberTabStorage, Label: "Storage"},
		{ID: MemberTabShards, Label: "Shards"},
		{ID: MemberTabEvents, Label: "Events"},
	}
}

func ClientTabs() []ClientTab {
	return []ClientTab{
		{ID: ClientTabOverview, Label: "Overview"},
		{ID: ClientTabIdentity, Label: "Identity"},
		{ID: ClientTabCapabilities, Label: "Capabilities"},
		{ID: ClientTabTraffic, Label: "Traffic"},
		{ID: ClientTabEvents, Label: "Events"},
	}
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

func compactMetricValue(value string) string {
	raw := strings.ReplaceAll(value, ",", "")
	if raw == "" {
		return value
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return value
		}
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed < 10000 {
		return value
	}
	return compactUnsigned(parsed)
}

func compactUnsigned(value uint64) string {
	units := []struct {
		suffix string
		value  uint64
	}{
		{suffix: "B", value: 1_000_000_000},
		{suffix: "M", value: 1_000_000},
		{suffix: "K", value: 1_000},
	}
	for _, unit := range units {
		if value >= unit.value {
			scaled := float64(value) / float64(unit.value)
			if scaled >= 100 || math.Mod(scaled, 1) == 0 {
				return fmt.Sprintf("%.0f%s", scaled, unit.suffix)
			}
			return fmt.Sprintf("%.1f%s", scaled, unit.suffix)
		}
	}
	return strconv.FormatUint(value, 10)
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
	case "OPERATION_STATE_PLANNED":
		return "status planned"
	case "OPERATION_STATE_RUNNING":
		return "status running"
	case "OPERATION_STATE_QUEUED":
		return "status queued"
	case "OPERATION_STATE_SUCCEEDED":
		return "status succeeded"
	case "OPERATION_STATE_FAILED":
		return "status failed"
	case "OPERATION_STATE_CANCELED", "OPERATION_STATE_EXPIRED":
		return "status canceled"
	default:
		return "status"
	}
}

func memberStateClass(state string) string {
	switch state {
	case "MEMBER_STATE_ONLINE":
		return "status succeeded"
	case "MEMBER_STATE_DRAINING":
		return "status queued"
	case "MEMBER_STATE_OFFLINE":
		return "status failed"
	default:
		return "status"
	}
}

func shortMemberState(state string) string {
	state = strings.TrimPrefix(state, "MEMBER_STATE_")
	state = strings.ToLower(strings.ReplaceAll(state, "_", " "))
	if state == "" {
		return "unknown"
	}
	return state
}

func memberDetailHref(memberID string) templ.SafeURL {
	return templ.SafeURL("/admin/members/" + url.PathEscape(memberID))
}

func memberTabHref(memberID, tab string) templ.SafeURL {
	return templ.SafeURL("/admin/members/" + url.PathEscape(memberID) + "/tab/" + url.PathEscape(tab))
}

func memberTabClass(active, tab string) string {
	if active == tab {
		return "filter-pill active"
	}
	return "filter-pill"
}

func clientDetailHref(clientID string) templ.SafeURL {
	return templ.SafeURL("/admin/clients/" + url.PathEscape(clientID))
}

func clientTabHref(clientID, tab string) templ.SafeURL {
	return templ.SafeURL("/admin/clients/" + url.PathEscape(clientID) + "/tab/" + url.PathEscape(tab))
}

func clientTabClass(active, tab string) string {
	if active == tab {
		return "filter-pill active"
	}
	return "filter-pill"
}

func evictionState(eviction EvictionSafetyData) string {
	if eviction.Error != "" || !eviction.Known {
		return "unknown"
	}
	if eviction.SafeToEvict {
		return "healthy"
	}
	return "warning"
}

func evictionText(eviction EvictionSafetyData) string {
	if eviction.Error != "" {
		return eviction.Error
	}
	if !eviction.Known {
		return "eviction safety is not available"
	}
	if eviction.SafeToEvict {
		return "safe to evict"
	}
	return "not safe to evict"
}

func evictionStatusClass(eviction EvictionSafetyData) string {
	switch evictionState(eviction) {
	case "healthy":
		return "status succeeded"
	case "warning":
		return "status queued"
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

func operationFilterClass(active, item string) string {
	if active == item {
		return "filter-pill active"
	}
	return "filter-pill"
}

func operationDetailHref(operationID string) templ.SafeURL {
	return templ.SafeURL("/admin/operations/" + url.PathEscape(operationID) + "/detail")
}

func operationDetailTarget(operationID string) string {
	return "#" + operationDetailTargetID(operationID)
}

func operationDetailTargetID(operationID string) string {
	return "operation-detail-" + safeHTMLID(operationID)
}

func safeHTMLID(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if isHTMLIDChar(char) {
			builder.WriteRune(char)
			continue
		}
		builder.WriteByte('-')
	}
	return builder.String()
}

func isHTMLIDChar(char rune) bool {
	return char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' ||
		char == '-' ||
		char == '_'
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
