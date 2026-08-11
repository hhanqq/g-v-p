package planner

import "context"

// groupMemberUsernames возвращает активных участников группы.
func (planner *Planner) groupMemberUsernames(ctx context.Context, groupID int64) ([]string, error) {
	rows, err := planner.pool.Query(ctx, `
		SELECT subscriber.trueconf_username
		FROM group_members member
		JOIN subscribers subscriber ON subscriber.id = member.subscriber_id
		WHERE member.group_id = $1 AND subscriber.active = TRUE
		ORDER BY subscriber.trueconf_username`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var usernames []string
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, err
		}
		usernames = append(usernames, username)
	}
	return usernames, rows.Err()
}
