package planner

import "context"

// resolveGroupsForProblem возвращает id групп, чья зона ответственности
// (group_equipment_scope) покрывает объект, на котором открыта проблема.
// Ось NULL не сужает — тот же паттерн, что cmdb_ownership/subscriptions.
func (planner *Planner) resolveGroupsForProblem(ctx context.Context, problem ProblemData) ([]int64, error) {
	if problem.ObjectID == nil {
		return nil, nil
	}
	rows, err := planner.pool.Query(ctx, `
		SELECT DISTINCT scope.group_id
		FROM group_equipment_scope scope
		JOIN cmdb_objects object ON object.id = $1
		WHERE (scope.object_id IS NULL OR scope.object_id = $1)
		  AND (scope.equipment_type IS NULL OR scope.equipment_type = object.equipment_type)
		  AND (scope.site IS NULL OR scope.site = object.site)`, *problem.ObjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groupIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, id)
	}
	return groupIDs, rows.Err()
}

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
