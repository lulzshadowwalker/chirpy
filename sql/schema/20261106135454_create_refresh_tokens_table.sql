-- +goose Up
create table if not exists refresh_tokens (
	token text primary key,
	user_id uuid not null,
	expires_at timestamp not null,
	revoked_at timestamp,
	created_at timestamp not null default now(),
	updated_at timestamp not null default now(),

	foreign key (user_id) references users(id) on delete cascade
);

-- +goose Down
drop table if exists refresh_tokens;
