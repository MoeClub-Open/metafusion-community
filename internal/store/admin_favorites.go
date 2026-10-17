package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AdminFavorite 是库内的复合键记录；所属账号或目录目标无法解析时仍可治理。
type AdminFavorite struct {
	OwnerID    string    `json:"owner_id"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type AdminFavoriteFilter struct {
	OwnerID    string
	TargetType string
	TargetID   string
}

// ListAdminFavorites 跨用户读取原始记录，不做目录解析、别名归一或可见性过滤。
func (s *Store) ListAdminFavorites(ctx context.Context, filter AdminFavoriteFilter, limit, offset int) ([]AdminFavorite, int, error) {
	if filter.TargetType != "" {
		if _, err := KindFor(filter.TargetType); err != nil {
			return nil, 0, err
		}
	}
	clauses := []string{"TRUE"}
	args := []any{}
	for _, part := range []struct{ column, value string }{
		{"user_id", filter.OwnerID},
		{"target_type", filter.TargetType},
		{"target_id", filter.TargetID},
	} {
		if part.value != "" {
			args = append(args, part.value)
			clauses = append(clauses, fmt.Sprintf("%s=$%d", part.column, len(args)))
		}
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM community.favorites WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT user_id::text, target_type, target_id::text, created_at
		FROM community.favorites WHERE %s
		ORDER BY created_at DESC, user_id, target_type, target_id LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []AdminFavorite{}
	for rows.Next() {
		var item AdminFavorite
		if err = rows.Scan(&item.OwnerID, &item.TargetType, &item.TargetID, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// DeleteAdminFavorite 只删除传入的复合键；RETURNING 保证审计快照就是实际删除的行。
// 不存在时返回 sql.ErrNoRows。
func (s *Store) DeleteAdminFavorite(ctx context.Context, ownerID, targetType, targetID string) (AdminFavorite, error) {
	if _, err := KindFor(targetType); err != nil {
		return AdminFavorite{}, err
	}
	var deleted AdminFavorite
	err := s.db.QueryRowContext(ctx, `DELETE FROM community.favorites
		WHERE user_id=$1 AND target_type=$2 AND target_id=$3
		RETURNING user_id::text, target_type, target_id::text, created_at`, ownerID, targetType, targetID).
		Scan(&deleted.OwnerID, &deleted.TargetType, &deleted.TargetID, &deleted.CreatedAt)
	return deleted, err
}
