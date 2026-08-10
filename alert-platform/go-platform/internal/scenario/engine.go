package scenario

import (
	"encoding/json"
	"time"
)

type Node struct {
	ID   string         `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type Edge struct {
	Source       string  `json:"source"`
	Target       string  `json:"target"`
	SourceHandle *string `json:"sourceHandle"`
}

type Graph struct {
	RootID string
	Nodes  map[string]Node
	Edges  map[string]map[string]*string
}

func Parse(raw string) (*Graph, bool) {
	var payload struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload.Nodes) == 0 {
		return nil, false
	}
	allowed := map[string]bool{
		"condition": true, "notify": true, "wait": true, "ack_check": true,
		"subscription_check": true, "availability_check": true,
	}
	nodes := make(map[string]Node, len(payload.Nodes))
	incoming := make(map[string]int, len(payload.Nodes))
	connections := make(map[string][]Edge, len(payload.Nodes))
	for _, node := range payload.Nodes {
		if node.ID == "" || !allowed[node.Type] {
			return nil, false
		}
		if _, exists := nodes[node.ID]; exists {
			return nil, false
		}
		nodes[node.ID], incoming[node.ID] = node, 0
	}
	for _, edge := range payload.Edges {
		if _, ok := nodes[edge.Source]; !ok {
			return nil, false
		}
		if _, ok := nodes[edge.Target]; !ok {
			return nil, false
		}
		connections[edge.Source] = append(connections[edge.Source], edge)
		incoming[edge.Target]++
	}
	roots := make([]string, 0, 1)
	for id, count := range incoming {
		if count == 0 {
			roots = append(roots, id)
		}
	}
	if len(roots) != 1 || nodes[roots[0]].Type != "condition" {
		return nil, false
	}
	edges := make(map[string]map[string]*string, len(nodes))
	for id, node := range nodes {
		out := connections[id]
		switch node.Type {
		case "ack_check", "subscription_check", "availability_check":
			edges[id] = map[string]*string{"yes": nil, "no": nil}
			for _, edge := range out {
				if edge.SourceHandle == nil || (*edge.SourceHandle != "yes" && *edge.SourceHandle != "no") || edges[id][*edge.SourceHandle] != nil {
					return nil, false
				}
				target := edge.Target
				edges[id][*edge.SourceHandle] = &target
			}
		case "notify":
			// Двухключевая карта: ребро без sourceHandle — "default" (все
			// существующие сохранённые графы парсятся без изменений), ребро
			// с sourceHandle="no_recipient" — опциональная явная ветка
			// эскалации, когда узел резолвит ноль получателей (см.
			// internal/planner/automation.go) вместо тихого пропуска.
			edges[id] = map[string]*string{"default": nil, "no_recipient": nil}
			for _, edge := range out {
				handle := "default"
				if edge.SourceHandle != nil {
					if *edge.SourceHandle != "no_recipient" {
						return nil, false
					}
					handle = "no_recipient"
				}
				if edges[id][handle] != nil {
					return nil, false
				}
				target := edge.Target
				edges[id][handle] = &target
			}
		default:
			if len(out) > 1 {
				return nil, false
			}
			edges[id] = map[string]*string{"default": nil}
			if len(out) == 1 {
				target := out[0].Target
				edges[id]["default"] = &target
			}
		}
	}
	color := make(map[string]uint8, len(nodes))
	var visit func(string) bool
	visit = func(id string) bool {
		color[id] = 1
		for _, target := range edges[id] {
			if target == nil {
				continue
			}
			if color[*target] == 1 || color[*target] == 0 && !visit(*target) {
				return false
			}
		}
		color[id] = 2
		return true
	}
	if !visit(roots[0]) {
		return nil, false
	}
	for id := range nodes {
		if color[id] != 2 {
			return nil, false
		}
	}
	return &Graph{RootID: roots[0], Nodes: nodes, Edges: edges}, true
}

// StepTrace — один реально пройденный узел за вызов Advance (в отличие
// от тиков, когда узел ack/wait переоценивается, но переход не
// происходит — те тики трассу не пополняют).
type StepTrace struct {
	NodeID   string
	NodeType string
	Branch   string // "default" | "yes" | "no" | "elapsed"
}

type Outcome struct {
	Kind          string
	Step          *Node
	CurrentNodeID string
	EnteredAt     time.Time
	Trace         []StepTrace
}

func Advance(current string, enteredAt time.Time, graph *Graph, problemStatus string, facts map[string]bool, now time.Time) Outcome {
	if problemStatus != "OPEN" && problemStatus != "FLAPPING" {
		return Outcome{Kind: "done", CurrentNodeID: current, EnteredAt: enteredAt}
	}
	nodeID := current
	if nodeID == graph.RootID {
		next := graph.Edges[nodeID]["default"]
		if next == nil {
			return Outcome{Kind: "done", CurrentNodeID: nodeID, EnteredAt: enteredAt}
		}
		nodeID, enteredAt = *next, now
	}
	var trace []StepTrace
	for range len(graph.Nodes) + 1 {
		node := graph.Nodes[nodeID]
		switch node.Type {
		case "notify":
			next := graph.Edges[nodeID]["default"]
			nextID := ""
			if next != nil {
				nextID = *next
			}
			trace = append(trace, StepTrace{NodeID: nodeID, NodeType: node.Type, Branch: "default"})
			return Outcome{Kind: "notify", Step: &node, CurrentNodeID: nextID, EnteredAt: now, Trace: trace}
		case "wait":
			minutes, _ := node.Data["minutes"].(float64)
			if now.Sub(enteredAt) < time.Duration(minutes)*time.Minute {
				return Outcome{Kind: "wait", CurrentNodeID: nodeID, EnteredAt: enteredAt, Trace: trace}
			}
			trace = append(trace, StepTrace{NodeID: nodeID, NodeType: node.Type, Branch: "elapsed"})
			next := graph.Edges[nodeID]["default"]
			if next == nil {
				return Outcome{Kind: "done", CurrentNodeID: nodeID, EnteredAt: enteredAt, Trace: trace}
			}
			nodeID, enteredAt = *next, now
		case "ack_check", "subscription_check", "availability_check":
			branch := "no"
			if facts[nodeID] {
				branch = "yes"
			}
			trace = append(trace, StepTrace{NodeID: nodeID, NodeType: node.Type, Branch: branch})
			next := graph.Edges[nodeID][branch]
			if next == nil {
				return Outcome{Kind: "done", CurrentNodeID: nodeID, EnteredAt: enteredAt, Trace: trace}
			}
			nodeID, enteredAt = *next, now
		default:
			return Outcome{Kind: "done", CurrentNodeID: nodeID, EnteredAt: enteredAt, Trace: trace}
		}
	}
	return Outcome{Kind: "done", CurrentNodeID: nodeID, EnteredAt: enteredAt, Trace: trace}
}
