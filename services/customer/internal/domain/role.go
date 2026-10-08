package domain

type Role string

const (
	RoleClient  Role = "CLIENT"
	RoleOfficer Role = "OFFICER"
	RoleAdmin   Role = "ADMIN"
)

func (r Role) Valid() bool {
	switch r {
	case RoleClient, RoleOfficer, RoleAdmin:
		return true
	default:
		return false
	}
}

func (r Role) IsStaff() bool {
	switch r {
	case RoleOfficer, RoleAdmin:
		return true
	default:
		return false
	}
}
