package store

import (
	"errors"
	"math"
	"strings"
	"time"
)

// Replace polling-day estimates atomically, never add daily totals to them.
// A failed/partial response leaves every existing financial amount intact.
func (s *Store) SaveEJHDailyConsumption(expected EJHConsumption, amounts map[string]float64, now time.Time, failure string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current EJHConsumption
	if err = s.ejhGetTx(tx, "finance_usage", expected.ID, &current); err != nil {
		return err
	}
	if current.ConnectionIdentity != expected.ConnectionIdentity || current.DownstreamID != expected.DownstreamID || current.StartDay != expected.StartDay || current.EndDay != expected.EndDay || !sameFinanceTime(current.SourceAt, expected.SourceAt) {
		return errors.New("消费采样已更新，等待下一轮按日核对")
	}
	current.DailyAttemptAt, current.DailyError = &now, failure
	if failure != "" {
		if err = s.ejhPutTx(tx, "finance_usage", current.ID, current); err != nil {
			return err
		}
		return tx.Commit()
	}
	start, e1 := time.ParseInLocation("2006-01-02", current.StartDay, financeZone)
	end, e2 := time.ParseInLocation("2006-01-02", current.EndDay, financeZone)
	if e1 != nil || e2 != nil || end.Before(start) || end.Sub(start) > 90*24*time.Hour {
		return errors.New("消费日期范围无效")
	}
	total := 0.0
	count := 0
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		amount, ok := amounts[day.Format("2006-01-02")]
		if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
			return errors.New("每日消费响应不完整")
		}
		total += amount
		count++
	}
	if len(amounts) != count || math.IsInf(total, 0) {
		return errors.New("每日消费范围不匹配")
	}
	// A downstream retention purge must not erase already known history. Do not
	// spread an unknown remainder over arbitrary dates to make totals match.
	if total+0.000001 < current.Total {
		return errors.New("下游每日合计低于已记录累计，历史数据待核实")
	}
	rows, err := tx.Query("SELECT id FROM ejh_records WHERE kind='finance_usage_day'")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(id, current.ID+":") {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec("DELETE FROM ejh_records WHERE kind='finance_usage_day' AND id=?", id); err != nil {
			return err
		}
	}
	for day, amount := range amounts {
		id := current.ID + ":" + day
		entry := EJHConsumptionDay{ID: id, Email: current.Email, Day: day, Amount: amount, At: now, Source: "sub2_daily"}
		if err = s.ejhPutTx(tx, "finance_usage_day", id, entry); err != nil {
			return err
		}
	}
	current.Total = max(current.Total, total)
	current.Baseline = max(0, current.Total-total)
	current.DailyCheckedAt, current.DailySourceAt, current.At = &now, current.SourceAt, now
	if err = s.ejhPutTx(tx, "finance_usage", current.ID, current); err != nil {
		return err
	}
	return tx.Commit()
}

func sameFinanceTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
