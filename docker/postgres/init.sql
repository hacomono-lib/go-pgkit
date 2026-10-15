-- Create roles
CREATE ROLE migration_user WITH LOGIN PASSWORD 'migration_password' SUPERUSER;
CREATE ROLE app_user WITH LOGIN PASSWORD 'app_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
-- Password with characters that need quoting in a keyword/value connection string
CREATE ROLE special_password_user WITH LOGIN PASSWORD 'timezone=x p a\s''s\word\'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;

-- Grant privileges to app_user
GRANT ALL PRIVILEGES ON DATABASE pgkit_test TO migration_user;
GRANT CONNECT ON DATABASE pgkit_test TO app_user;
GRANT CONNECT ON DATABASE pgkit_test TO special_password_user;
GRANT USAGE ON SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES FOR ROLE migration_user IN SCHEMA public
  GRANT ALL PRIVILEGES ON TABLES TO app_user;
ALTER DEFAULT PRIVILEGES FOR ROLE migration_user IN SCHEMA public
  GRANT ALL PRIVILEGES ON SEQUENCES TO app_user;
