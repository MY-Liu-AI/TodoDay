-- 0002_focus rollback: drops the focus_sessions table (all pomodoro history is lost).

DROP TABLE IF EXISTS focus_sessions;
