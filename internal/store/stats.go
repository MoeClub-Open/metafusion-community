package store

import "context"

// UserStats 是仓储层的三个原始互动计数。HTTP 层按账号隐私规则决定是否公开收藏计数。
type UserStats struct {
	TopicsCreated   int `json:"topics_created"`
	CommentsCreated int `json:"comments_created"`
	FavoritesCount  int `json:"favorites_count"`
}

// StatsFor 用一条 SQL 取回三个数字（三个标量子查询）：三次往返会把一个"给人看的统计"
// 变成三个可能互相不一致的读数，也没有必要。
//
// 口径（每一项都与对应的列表接口同源，不制造第二套数字）：
//
//   - topics_created：community.topics 里 author_id 本人、且**非评论板块**（board_code <> 评论板块）的行数。
//     评论板块的行是"实体短评"，不是主题——主题列表同样把评论排除在外（见 handler 的 commentBoard）。
//   - comments_created：community.posts 里 author_id 本人的行数，即**楼中回复**（前端标签是"互动回复"）。
//     主题正文不算回复（它不在 posts 表里，已计入 topics_created）；实体短评两边都不重复计——
//     宁可少算，也不让同一行在两个数字里各出现一次。
//   - favorites_count：community.favorites 里 user_id 本人的行数。只有本人或账号服务确认
//     favorites_public=true 时 HTTP 层才返回数字，否则返回 null，不泄露私有收藏数量。
//     公开列表跳过不可见目标但不影响 total；本服务不读取账号或目录 schema。
//
// commentBoard 由调用方传入（handler 的 commentBoard 常量）：板块码只有一份来源，
// 将来改名不会出现"接口认、统计不认"的两套过滤条件。
func (s *Store) StatsFor(ctx context.Context, userID, commentBoard string) (UserStats, error) {
	var out UserStats
	err := s.db.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM community.topics WHERE author_id=$1::uuid AND board_code <> $2),
		  (SELECT count(*) FROM community.posts WHERE author_id=$1::uuid),
		  (SELECT count(*) FROM community.favorites WHERE user_id=$1::uuid)`,
		userID, commentBoard).Scan(&out.TopicsCreated, &out.CommentsCreated, &out.FavoritesCount)
	return out, err
}
