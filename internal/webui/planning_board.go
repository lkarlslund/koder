package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

const planningBoardAssetPath = "assets/board.html"

var planningBoardHTML = mustReadAsset(planningBoardAssetPath)

type planningBoardResponse struct {
	SessionID   id.ID                      `json:"session_id"`
	ProjectRoot string                     `json:"project_root"`
	Plan        planning.Plan              `json:"plan"`
	Tasks       []planning.Task            `json:"tasks"`
	TasksByKey  map[string][]planning.Task `json:"tasks_by_milestone"`
}

type boardMilestoneRequest struct {
	Key          string `json:"key"`
	Title        string `json:"title"`
	Notes        string `json:"notes"`
	Status       string `json:"status"`
	DependsOnKey string `json:"depends_on_key"`
	Position     *int   `json:"position"`
}

type boardMilestoneOrderRequest struct {
	Keys []string `json:"keys"`
}

type boardTaskAddRequest struct {
	MilestoneKey string `json:"milestone_key"`
	Content      string `json:"content"`
}

type boardTaskUpdateRequest struct {
	TaskKey      string `json:"task_key"`
	MilestoneKey string `json:"milestone_key"`
	Status       string `json:"status"`
	Content      string `json:"content"`
	Note         string `json:"note"`
	Position     *int   `json:"position"`
}

type boardStartChatRequest struct {
	ParentChatID id.ID  `json:"parent_chat_id"`
	MilestoneKey string `json:"milestone_key"`
	TaskKey      string `json:"task_key,omitempty"`
	Title        string `json:"title,omitempty"`
	Objective    string `json:"objective,omitempty"`
}

func renderPlanningBoardHTML() string {
	return strings.ReplaceAll(planningBoardHTML, assetHashPlaceholder, currentAssetHash)
}

func planningBoardSessionFromPath(rawPath string) (id.ID, bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(rawPath), "/"), "/")
	if len(parts) != 3 || parts[0] != "s" || parts[2] != "board" || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return id.ID(strings.TrimSpace(parts[1])), true
}

func (s *Server) handleSessionAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(r.URL.Path), "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "sessions" {
		http.NotFound(w, r)
		return
	}
	sessionID := id.ID(strings.TrimSpace(parts[2]))
	if sessionID == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	switch parts[3] {
	case "files":
		s.handleSessionFilesAPI(w, r, sessionID, parts[4:])
	case "board":
		s.handlePlanningBoardAPI(w, r, sessionID, parts[4:])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handlePlanningBoardAPI(w http.ResponseWriter, r *http.Request, sessionID id.ID, parts []string) {
	if len(parts) == 0 {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handlePlanningBoardState(w, r, sessionID)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch strings.Join(parts, "/") {
	case "milestones":
		s.handlePlanningBoardMilestone(w, r, sessionID)
	case "milestones/order":
		s.handlePlanningBoardMilestoneOrder(w, r, sessionID)
	case "tasks":
		s.handlePlanningBoardTaskAdd(w, r, sessionID)
	case "tasks/update":
		s.handlePlanningBoardTaskUpdate(w, r, sessionID)
	case "chats/start":
		s.handlePlanningBoardStartChat(w, r, sessionID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handlePlanningBoardState(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	resp, err := s.planningBoardState(r.Context(), sessionID)
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	writeFileBrowserJSON(w, r, resp)
}

func (s *Server) planningBoardState(ctx context.Context, sessionID id.ID) (planningBoardResponse, error) {
	session, err := s.controller.SessionByID(ctx, sessionID)
	if err != nil {
		return planningBoardResponse{}, err
	}
	plan, err := s.controller.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return planningBoardResponse{}, err
	}
	tasks, err := s.controller.ListTasks(ctx, sessionID, "")
	if err != nil {
		return planningBoardResponse{}, err
	}
	tasksByKey := map[string][]planning.Task{}
	for _, task := range tasks {
		tasksByKey[task.MilestoneKey] = append(tasksByKey[task.MilestoneKey], task)
	}
	for key, items := range tasksByKey {
		planning.SortTasks(items)
		tasksByKey[key] = items
	}
	return planningBoardResponse{
		SessionID:   sessionID,
		ProjectRoot: session.ProjectRoot,
		Plan:        plan,
		Tasks:       tasks,
		TasksByKey:  tasksByKey,
	}, nil
}

func (s *Server) handlePlanningBoardMilestone(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	var req boardMilestoneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode milestone: %v", err), http.StatusBadRequest)
		return
	}
	plan, err := s.controller.GetMilestonePlan(r.Context(), sessionID)
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	next, err := upsertBoardMilestone(plan, req)
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	if _, err := s.controller.SetMilestonePlan(r.Context(), sessionID, next.Summary, next.Milestones); err != nil {
		writePlanningBoardError(w, err)
		return
	}
	s.handlePlanningBoardState(w, r, sessionID)
}

func upsertBoardMilestone(plan planning.Plan, req boardMilestoneRequest) (planning.Plan, error) {
	now := time.Now().UTC()
	key := strings.TrimSpace(req.Key)
	title := strings.TrimSpace(req.Title)
	notes := strings.TrimSpace(req.Notes)
	dependsOnKey := strings.TrimSpace(req.DependsOnKey)
	status := planning.MilestoneStatusPending
	if raw := strings.TrimSpace(req.Status); raw != "" {
		parsed, err := planning.ParseMilestoneStatus(raw)
		if err != nil {
			return planning.Plan{}, err
		}
		status = parsed
	}
	next := plan
	next.Milestones = slices.Clone(plan.Milestones)
	if key == "" {
		if title == "" {
			return planning.Plan{}, fmt.Errorf("title is required")
		}
		position := len(next.Milestones)
		if req.Position != nil {
			position = *req.Position
		}
		next.Milestones = append(next.Milestones, planning.Milestone{
			Title:        title,
			Notes:        notes,
			Status:       status,
			DependsOnKey: dependsOnKey,
			Position:     position,
		})
	} else {
		found := false
		for idx := range next.Milestones {
			if planning.MilestoneKey(next.Milestones[idx]) != key {
				continue
			}
			found = true
			if title != "" {
				next.Milestones[idx].Title = title
			}
			next.Milestones[idx].Notes = notes
			next.Milestones[idx].Status = status
			next.Milestones[idx].DependsOnKey = dependsOnKey
			if req.Position != nil {
				next.Milestones[idx].Position = *req.Position
			}
			break
		}
		if !found {
			return planning.Plan{}, fmt.Errorf("milestone %s not found", key)
		}
	}
	normalizeMilestonePositions(next.Milestones)
	next.UpdatedAt = now
	next, _ = planning.NormalizePlanKeys(next)
	if err := planning.ValidateMilestoneProgress(next.Milestones); err != nil {
		return planning.Plan{}, err
	}
	return next, nil
}

func (s *Server) handlePlanningBoardMilestoneOrder(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	var req boardMilestoneOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode milestone order: %v", err), http.StatusBadRequest)
		return
	}
	plan, err := s.controller.GetMilestonePlan(r.Context(), sessionID)
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	order := map[string]int{}
	for idx, key := range req.Keys {
		if key = strings.TrimSpace(key); key != "" {
			order[key] = idx
		}
	}
	next := plan
	next.Milestones = slices.Clone(plan.Milestones)
	for idx := range next.Milestones {
		if position, ok := order[planning.MilestoneKey(next.Milestones[idx])]; ok {
			next.Milestones[idx].Position = position
		}
	}
	normalizeMilestonePositions(next.Milestones)
	if _, err := s.controller.SetMilestonePlan(r.Context(), sessionID, next.Summary, next.Milestones); err != nil {
		writePlanningBoardError(w, err)
		return
	}
	s.handlePlanningBoardState(w, r, sessionID)
}

func (s *Server) handlePlanningBoardTaskAdd(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	var req boardTaskAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode task: %v", err), http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	if _, err := s.controller.AddTasks(r.Context(), sessionID, strings.TrimSpace(req.MilestoneKey), []string{content}); err != nil {
		writePlanningBoardError(w, err)
		return
	}
	s.handlePlanningBoardState(w, r, sessionID)
}

func (s *Server) handlePlanningBoardTaskUpdate(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	var req boardTaskUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode task update: %v", err), http.StatusBadRequest)
		return
	}
	taskKey := strings.TrimSpace(req.TaskKey)
	if taskKey == "" {
		http.Error(w, "task_key is required", http.StatusBadRequest)
		return
	}
	tasks, err := s.controller.ListTasks(r.Context(), sessionID, "")
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	current, ok := findBoardTask(tasks, taskKey)
	if !ok {
		writePlanningBoardError(w, fmt.Errorf("task %s not found", taskKey))
		return
	}
	status := current.Status
	if raw := strings.TrimSpace(req.Status); raw != "" {
		status, err = planning.ParseTaskStatus(raw)
		if err != nil {
			writePlanningBoardError(w, err)
			return
		}
	}
	milestoneKey := strings.TrimSpace(req.MilestoneKey)
	if milestoneKey == "" {
		milestoneKey = current.MilestoneKey
	}
	if req.Position != nil || milestoneKey != current.MilestoneKey {
		position := current.Position
		if req.Position != nil {
			position = *req.Position
		}
		if _, err := s.controller.MoveTask(r.Context(), sessionID, taskKey, milestoneKey, status, position, req.Note); err != nil {
			writePlanningBoardError(w, err)
			return
		}
	} else if _, err := s.controller.UpdateTask(r.Context(), sessionID, id.ID(taskKey), status, req.Content, req.Note); err != nil {
		writePlanningBoardError(w, err)
		return
	}
	s.handlePlanningBoardState(w, r, sessionID)
}

func findBoardTask(tasks []planning.Task, taskKey string) (planning.Task, bool) {
	for _, task := range tasks {
		if planning.TaskKey(task) == taskKey {
			return task, true
		}
	}
	return planning.Task{}, false
}

func (s *Server) handlePlanningBoardStartChat(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	var req boardStartChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode chat start: %v", err), http.StatusBadRequest)
		return
	}
	parentChatID := id.ID(strings.TrimSpace(string(req.ParentChatID)))
	if parentChatID == "" {
		writePlanningBoardError(w, fmt.Errorf("parent_chat_id is required"))
		return
	}
	milestoneKey := strings.TrimSpace(req.MilestoneKey)
	if milestoneKey == "" {
		writePlanningBoardError(w, fmt.Errorf("milestone_key is required"))
		return
	}
	plan, err := s.controller.GetMilestonePlan(r.Context(), sessionID)
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	var milestone planning.Milestone
	found := false
	for _, item := range plan.Milestones {
		if planning.MilestoneKey(item) == milestoneKey {
			milestone = item
			found = true
			break
		}
	}
	if !found {
		writePlanningBoardError(w, fmt.Errorf("milestone %s not found", milestoneKey))
		return
	}
	objective := strings.TrimSpace(req.Objective)
	if objective == "" {
		objective = "Execute milestone " + milestoneKey + ": " + strings.TrimSpace(milestone.Title)
	}
	if taskKey := strings.TrimSpace(req.TaskKey); taskKey != "" {
		tasks, err := s.controller.ListTasks(r.Context(), sessionID, milestoneKey)
		if err != nil {
			writePlanningBoardError(w, err)
			return
		}
		task, ok := findBoardTask(tasks, taskKey)
		if !ok {
			writePlanningBoardError(w, fmt.Errorf("task %s not found", taskKey))
			return
		}
		objective = "Execute task " + taskKey + " in milestone " + milestoneKey + ": " + task.Content
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = milestoneKey + " worker"
	}
	status, err := s.controller.StartChat(r.Context(), sessionID, parentChatID, chattool.StartRequest{
		Profile:      chatrole.Execution,
		Objective:    objective,
		Title:        title,
		MilestoneKey: milestoneKey,
	})
	if err != nil {
		writePlanningBoardError(w, err)
		return
	}
	writeFileBrowserJSON(w, r, status)
}

func normalizeMilestonePositions(items []planning.Milestone) {
	slices.SortFunc(items, func(a, b planning.Milestone) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(planning.MilestoneKey(a), planning.MilestoneKey(b))
	})
	for idx := range items {
		items[idx].Position = idx
	}
}

func writePlanningBoardError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}
