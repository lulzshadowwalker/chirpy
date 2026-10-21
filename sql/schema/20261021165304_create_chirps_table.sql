
-- +goose Up
create table if not exists chirps (
    id uuid primary key default gen_random_uuid(),
    body text not null,
    user_id uuid not null,
    created_at timestamp not null default now(),
    updated_at timestamp not null default now(),

    foreign key (user_id) references users(id)
);

-- +goose Down
drop table if exists chirps;
