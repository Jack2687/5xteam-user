package vm2api

import (
	"context"
	"errors"
	"time"
)

type QualitySlotRef struct {
	ID           string `json:"id"`
	AssignmentID string `json:"assignment_id"`
}
type QualityResult struct {
	ID            int64     `json:"id"`
	Revision      string    `json:"revision"`
	Status        string    `json:"status"`
	Source        string    `json:"source"`
	StartedAt     time.Time `json:"started_at"`
	CheckedAt     time.Time `json:"checked_at"`
	QuestionID    string    `json:"question_id"`
	QuestionName  string    `json:"question_name"`
	Model         string    `json:"model"`
	Answer        string    `json:"answer"`
	Reason        string    `json:"reason"`
	DurationMS    int64     `json:"duration_ms"`
	ContentPassed *bool     `json:"content_passed"`
	TimePassed    *bool     `json:"time_passed"`
}
type QualitySlot struct {
	QualitySlotRef
	Identity string         `json:"identity"`
	Enabled  bool           `json:"enabled"`
	Running  bool           `json:"running"`
	Current  bool           `json:"current"`
	Error    string         `json:"error"`
	Result   *QualityResult `json:"result"`
}
type QualitySnapshot struct {
	Version   int           `json:"version"`
	ServerNow time.Time     `json:"server_now"`
	Revision  string        `json:"revision"`
	Slots     []QualitySlot `json:"slots"`
}

func (c *Client) QualityCapabilities(ctx context.Context) error {
	var v struct {
		Version         int  `json:"version"`
		ReadOnly        bool `json:"read_only"`
		AssignmentGuard bool `json:"assignment_guard"`
	}
	if err := c.request(ctx, "GET", "/vm-quality/capabilities", "", nil, &v); err != nil {
		return err
	}
	if v.Version != 1 || !v.ReadOnly || !v.AssignmentGuard {
		return errors.New("请先更新 vm2api：当前版本不支持安全读取降智结果")
	}
	return nil
}
func (c *Client) QualityResults(ctx context.Context, slots []QualitySlotRef) (QualitySnapshot, error) {
	var v QualitySnapshot
	if err := c.request(ctx, "POST", "/vm-quality/results", "", map[string]any{"slots": slots}, &v); err != nil {
		return v, err
	}
	if v.Version != 1 || v.Revision == "" || v.ServerNow.IsZero() {
		return v, errors.New("vm2api 降智结果接口版本或响应无效，请更新 vm2api")
	}
	if delta := time.Since(v.ServerNow); delta > 5*time.Minute || delta < -5*time.Minute {
		return v, errors.New("vm2api 与本机时间相差超过5分钟，暂停自动处理")
	}
	return v, nil
}
