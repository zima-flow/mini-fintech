CREATE ROLE example_app WITH LOGIN PASSWORD 'example_app';
CREATE ROLE auth_app WITH LOGIN PASSWORD 'auth_app';
CREATE ROLE customer_app WITH LOGIN PASSWORD 'customer_app';

CREATE DATABASE example OWNER example_app;
CREATE DATABASE auth OWNER auth_app;
CREATE DATABASE customer OWNER customer_app;

\connect example
GRANT ALL ON SCHEMA public TO example_app;

\connect auth
GRANT ALL ON SCHEMA public TO auth_app;

\connect customer
GRANT ALL ON SCHEMA public TO customer_app;
