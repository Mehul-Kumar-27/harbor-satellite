-- +goose Up

CREATE TABLE robot_projects (
  robot_id INT REFERENCES robots(id) ON DELETE CASCADE,
  project_id INT REFERENCES projects(id) ON DELETE CASCADE,
  PRIMARY KEY (robot_id, project_id)
);

-- +goose Down
DROP TABLE robot_projects;
