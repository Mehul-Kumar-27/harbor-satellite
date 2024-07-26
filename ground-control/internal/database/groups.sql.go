// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.26.0
// source: groups.sql

package database

import (
	"context"
	"time"
)

const createGroup = `-- name: CreateGroup :one
INSERT INTO groups (group_name, created_at, updated_at)
VALUES ($1, $2, $3)
RETURNING id, group_name, created_at, updated_at
`

type CreateGroupParams struct {
	GroupName string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (q *Queries) CreateGroup(ctx context.Context, arg CreateGroupParams) (Group, error) {
	row := q.db.QueryRowContext(ctx, createGroup, arg.GroupName, arg.CreatedAt, arg.UpdatedAt)
	var i Group
	err := row.Scan(
		&i.ID,
		&i.GroupName,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const getGroupByID = `-- name: GetGroupByID :one
SELECT id, group_name, created_at, updated_at FROM groups
WHERE id = $1
`

func (q *Queries) GetGroupByID(ctx context.Context, id int32) (Group, error) {
	row := q.db.QueryRowContext(ctx, getGroupByID, id)
	var i Group
	err := row.Scan(
		&i.ID,
		&i.GroupName,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const getGroupByName = `-- name: GetGroupByName :one
SELECT id, group_name, created_at, updated_at FROM groups
WHERE group_name = $1
`

func (q *Queries) GetGroupByName(ctx context.Context, groupName string) (Group, error) {
	row := q.db.QueryRowContext(ctx, getGroupByName, groupName)
	var i Group
	err := row.Scan(
		&i.ID,
		&i.GroupName,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listGroups = `-- name: ListGroups :many
SELECT id, group_name, created_at, updated_at FROM groups
`

func (q *Queries) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := q.db.QueryContext(ctx, listGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Group
	for rows.Next() {
		var i Group
		if err := rows.Scan(
			&i.ID,
			&i.GroupName,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
