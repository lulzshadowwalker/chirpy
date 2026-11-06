-- name: CreateRefreshToken :one
insert into refresh_tokens (token, user_id, expires_at)
values ($1, $2, $3)
returning *;

-- name: GetRefreshToken :one
select * from refresh_tokens
where token = $1;

-- name: UpdateRefreshToken :one
update refresh_tokens
set updated_at = $1,
revoked_at = $2
returning *;

-- name: GetUserFromRefreshToken :one
select u.* from users u
left join refresh_tokens r
on r.user_id = u.id
where r.token = $1
and r.revoked_at is null
and r.expires_at > now();
