package domain

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hasura/go-graphql-client"
)

var ErrNotFound = fmt.Errorf("not found")

func setAuthHeader(secret string) func(req *http.Request) {
	return func(req *http.Request) {
		req.Header.Add("x-hasura-admin-secret", secret)
	}
}

type Hasura struct {
	client *graphql.Client
}

func NewClient(hasuraAdminSecret, hasuraEndPoint string) *Hasura {
	return &Hasura{
		client: graphql.NewClient(hasuraEndPoint, nil).
			WithRequestModifier(setAuthHeader(hasuraAdminSecret)),
	}
}

func (h *Hasura) CreateUser(ctx context.Context, stid, name, email, ugGuid string, groupID int) error {
	// mutation InsertUserOne($stguid: String, $name: String, $email: String, $ugGuid: String, $groupID: Int) {
	// 	insert_user_one(object: {guid: $stguid, name: $name, email: $email, user_groups: {data: {group_id: $groupID, guid: $ugGuid, user_guid: $stguid}}}) {
	// 	  role
	// 	}
	// }
	var m struct {
		InsertUserOne struct {
			Guid graphql.String
		} `graphql:"insert_user_one(object: {guid: $stguid, name: $name, email: $email, user_groups: {data: {group_id: $groupID, guid: $ugGuid}}})"`
	}
	variables := map[string]interface{}{
		"stguid":  graphql.String(stid),
		"name":    graphql.String(name),
		"email":   graphql.String(email),
		"ugGuid":  graphql.String(ugGuid),
		"groupID": graphql.Int(groupID),
	}

	if err := h.client.Mutate(ctx, &m, variables); err != nil {
		return err
	}
	return nil
}

func (h *Hasura) GetUser(ctx context.Context, guid string) (int32, error) {
	var q struct {
		GetUser struct {
			Role graphql.Int
		} `graphql:"user_by_pk(guid: $guid)"`
	}
	variables := map[string]interface{}{
		"guid": graphql.String(guid),
	}

	if err := h.client.Query(ctx, &q, variables); err != nil {
		return 0, err
	}

	return int32(q.GetUser.Role), nil
}

func (h *Hasura) GetUserByEmail(ctx context.Context, email string) (string, error) {
	var q struct {
		User []struct {
			Guid graphql.String
		} `graphql:"user(where: {email: {_eq: $email}})"`
	}
	variables := map[string]interface{}{
		"email": graphql.String(email),
	}

	if err := h.client.Query(ctx, &q, variables); err != nil {
		return "", err
	}

	if len(q.User) == 0 {
		return "", ErrNotFound

	}
	if len(q.User) > 1 {
		return "", fmt.Errorf("failed to get user")

	}
	return string(q.User[0].Guid), nil
}

func (h *Hasura) GetUserGroupRole(ctx context.Context, userGUID, groupGUID string) (int32, error) {
	// query GetUserGroupRole($userGUID: String, $groupGUID: String) {
	// 	user(where: {guid: {_eq: $userGUID}}) {
	// 	  user_groups(where: {group: {guid: {_eq: $groupGUID}}}) {
	// 		role
	// 	  }
	// 	}
	// }
	var query struct {
		User []struct {
			UserGroups []struct {
				Role graphql.Int
			} `graphql:"user_groups(where: {group: {guid: {_eq: $groupGUID}}})"`
		} `graphql:"user(where: {guid: {_eq: $userGUID}})"`
	}
	variables := map[string]interface{}{
		"userGUID":  graphql.String(userGUID),
		"groupGUID": graphql.String(groupGUID),
	}

	if err := h.client.Query(ctx, &query, variables); err != nil {
		return 0, err
	}

	if len(query.User) != 1 {
		return 0, fmt.Errorf("user is not found")
	}

	if len(query.User[0].UserGroups) != 1 {
		return 0, fmt.Errorf("user group is not found")
	}
	return int32(query.User[0].UserGroups[0].Role), nil
}

const (
	User  uint32 = 1
	Owner uint32 = 2
	Super uint32 = 3
)

func GetHasuraRole(r int32) string {
	if r == int32(Owner) {
		return "owner"
	} else if r == int32(Super) {
		return "super"
	}
	return "user"
}
