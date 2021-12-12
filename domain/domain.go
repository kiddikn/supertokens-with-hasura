package domain

import (
	"context"
	"net/http"
	"time"

	"github.com/hasura/go-graphql-client"
)

type CustomTransport struct {
	adminSecret string
	base        http.RoundTripper
}

func (ct *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("x-hasura-admin-secret", ct.adminSecret)
	return ct.base.RoundTrip(req)
}

type Hasura struct {
	client *graphql.Client
}

func NewClient(hasuraAdminSecret, hasuraEndPoint string) *Hasura {
	tr := &CustomTransport{
		adminSecret: hasuraAdminSecret,
		base: &http.Transport{
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	c := &http.Client{Transport: tr}
	client := graphql.NewClient(hasuraEndPoint, c)
	return &Hasura{
		client: client,
	}
}

func (h *Hasura) CreateUser(id, name, email string) error {
	var m struct {
		InsertUsersOne struct{} `graphql:"insert_users_one(object: {guid: $guid, name: $name, email: $email})"`
	}
	variables := map[string]interface{}{
		"guid":  graphql.String(id),
		"name":  graphql.String(name),
		"email": graphql.String(email),
	}

	if err := h.client.Mutate(context.Background(), &m, variables); err != nil {
		return err
	}
	return nil
}

func (h *Hasura) GetUser(guid string) (int32, error) {
	var q struct {
		GetUser struct {
			Role graphql.Int
		} `graphql:"user_by_pk(guid: $guid)"`
	}
	variables := map[string]interface{}{
		"guid": graphql.String(guid),
	}

	if err := h.client.Query(context.Background(), &q, variables); err != nil {
		return 0, err
	}

	return int32(q.GetUser.Role), nil
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
