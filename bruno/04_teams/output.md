## 📄 04_list_team_members.bru
```
meta {
  name: List Team Members
  type: http
  seq: 4
}

get {
  url: {{base_url}}/teams/{{team_id}}/members
  body: none
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

```

## 📄 06_remove_team_member.bru
```
meta {
  name: Remove Team Member
  type: http
  seq: 6
}

delete {
  url: {{base_url}}/teams/{{team_id}}/members/{{team_member_id}}
  body: none
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

```

## 📄 03_get_team.bru
```
meta {
  name: Get Team
  type: http
  seq: 3
}

get {
  url: {{base_url}}/teams/{{team_id}}
  body: none
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

```

## 📄 01_list_teams.bru
```
meta {
  name: List Teams
  type: http
  seq: 1
}

get {
  url: {{base_url}}/teams
  body: none
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

```

## 📄 07_delete_team.bru
```
meta {
  name: Delete Team
  type: http
  seq: 7
}

delete {
  url: {{base_url}}/teams/{{team_id}}
  body: none
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

```

## 📄 00_invite_member.bru
```
meta {
  name: Invite Member for Teams
  type: http
  seq: 0
}

post {
  url: {{base_url}}/users/invite
  body: json
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

body:json {
  {
    "email": "member_teams@baleia.io",
    "role": "member"
  }
}

vars:post-response {
  team_member_id: res.body.id
}
```

## 📄 05_add_team_member.bru
```
meta {
  name: Add Team Member
  type: http
  seq: 5
}

post {
  url: {{base_url}}/teams/{{team_id}}/members
  body: json
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

body:json {
  {
    "user_id": "{{team_member_id}}",
    "role": "member"
  }
}

```

## 📄 02_create_team.bru
```
meta {
  name: Create Team
  type: http
  seq: 2
}

post {
  url: {{base_url}}/teams
  body: json
  auth: bearer
}

auth:bearer {
  token: {{token}}
}

body:json {
  {
    "name": "Engineering",
    "description": "Engineering team"
  }
}

vars:post-response {
  team_id: res.body.id
}

```

