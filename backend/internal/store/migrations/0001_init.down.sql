-- 0001_init rollback: drops every object created by 0001_init.up.sql.
-- Destructive: all user data in these tables is lost. Back up the database file first.

DROP TABLE IF EXISTS task_events;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS task_groups;
DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
