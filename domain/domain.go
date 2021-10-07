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

func (h *Hasura) CreateUser(id, name string) error {
	var m struct {
		InsertUsersOne struct {
			Id graphql.String
		} `graphql:"insert_users_one(object: {id: $id, name: $name, created_at: now})"`
	}
	variables := map[string]interface{}{
		"id":   graphql.String(id),
		"name": graphql.String(name),
	}

	if err := h.client.Mutate(context.Background(), &m, variables); err != nil {
		return err
	}
	return nil
}
