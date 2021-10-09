package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/caarlos0/env/v6"
	"github.com/kiddikn/supertokens-with-hasura/domain"
	"github.com/supertokens/supertokens-golang/recipe/emailpassword"
	"github.com/supertokens/supertokens-golang/recipe/emailpassword/epmodels"
	"github.com/supertokens/supertokens-golang/recipe/session"
	"github.com/supertokens/supertokens-golang/supertokens"
)

type config struct {
	Port              int    `env:"PORT"`
	SuperTokensURL    string `env:"SUPERTOKENS_URL,required,notEmpty"`
	SuperTokensApiKey string `env:"SUPERTOKENS_API_KEY,required,notEmpty"`
	AppName           string `env:"APP_NAME,required,notEmpty"`
	ApiDomain         string `env:"API_DOMAIN,required,notEmpty"`
	WebSiteDomain     string `env:"WEB_SITE_DOMAIN,required,notEmpty"`
	HasuraEndPoint    string `env:"HASURA_END_POINT_URL,required,notEmpty"`
	HasuraAdminSecret string `env:"HASURA_ADMIN_SECRET,required,notEmpty"`
}

func main() {
	cfg := config{}
	if err := env.Parse(&cfg); err != nil {
		log.Fatal(err)
	}

	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config) error {
	d := domain.NewClient(cfg.HasuraAdminSecret, cfg.HasuraEndPoint)

	if err := supertokens.Init(supertokens.TypeInput{
		Supertokens: &supertokens.ConnectionInfo{
			ConnectionURI: cfg.SuperTokensURL,
			APIKey:        cfg.SuperTokensApiKey,
		},
		AppInfo: supertokens.AppInfo{
			AppName:       cfg.AppName,
			APIDomain:     cfg.ApiDomain,
			WebsiteDomain: cfg.WebSiteDomain,
		},
		RecipeList: []supertokens.Recipe{
			emailpassword.Init(
				&epmodels.TypeInput{
					SignUpFeature: &epmodels.TypeInputSignUp{
						FormFields: []epmodels.TypeInputFormField{
							{
								ID: "name",
							},
						},
					},
					Override: &epmodels.OverrideStruct{
						APIs: func(originalImplementation epmodels.APIInterface) epmodels.APIInterface {
							// First we copy the original implementation
							newImplementation := originalImplementation

							newImplementation.SignUpPOST = func(formFields []epmodels.TypeFormField, options epmodels.APIOptions) (epmodels.SignUpResponse, error) {
								resp, err := originalImplementation.SignUpPOST(formFields, options)
								if err != nil {
									return epmodels.SignUpResponse{}, err
								}

								if resp.OK != nil {
									// sign up was successful
									id := resp.OK.User.ID
									var name string
									for _, ff := range formFields {
										if ff.ID == "name" {
											name = ff.Value
											break
										}
									}

									if err := d.CreateUser(id, name); err != nil {
										return epmodels.SignUpResponse{}, err
									}
								}

								return resp, err
							}

							return newImplementation
						},
					},
				},
			),
			session.Init(nil),
		},
	}); err != nil {
		return err
	}

	s, err := httpServer(cfg.Port, cfg.WebSiteDomain, cfg.HasuraEndPoint)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		s.Shutdown(ctx)
		log.Printf("shutdown ok")
	}()

	fmt.Printf("start http server: port is %d\n", cfg.Port)
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("failed to ListenAndServe of HTTP server: %v", err)
	}
	return nil
}

func httpServer(httpPort int, webSiteDomain, hasuraEndPoint string) (*http.Server, error) {
	httpEndpoint := fmt.Sprintf(":%d", httpPort)
	return &http.Server{
		Addr: httpEndpoint,
		Handler: corsMiddleware(
			supertokens.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/verify" {
					fmt.Printf("request path: %s\n", r.URL.Path)
					session.VerifySession(nil, sessioninfo).ServeHTTP(rw, r)
					return
				}
			})),
			webSiteDomain,
			hasuraEndPoint,
		),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}, nil
}

func corsMiddleware(next http.Handler, webSiteDomain, hasuraEndPoint string) http.Handler {
	allowHost := func(host string) string {
		fmt.Println(host)
		for _, a := range []string{webSiteDomain, hasuraEndPoint} {
			if host == a {
				return host
			}
		}
		return ""
	}

	return http.HandlerFunc(func(response http.ResponseWriter, r *http.Request) {
		response.Header().Set("Access-Control-Allow-Origin", allowHost(r.Host))
		response.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == "OPTIONS" {
			response.Header().Set("Access-Control-Allow-Headers",
				strings.Join(append([]string{"Content-Type"},
					supertokens.GetAllCORSHeaders()...), ","))
			response.Header().Set("Access-Control-Allow-Methods", "*")
			response.Write([]byte(""))
		} else {
			next.ServeHTTP(response, r)
		}
	})
}

func sessioninfo(w http.ResponseWriter, r *http.Request) {
	sessionContainer := session.GetSessionFromRequestContext(r.Context())
	if sessionContainer == nil {
		w.WriteHeader(500)
		w.Write([]byte("no session found"))
		return
	}

	w.WriteHeader(200)
	w.Header().Add("content-type", "application/json")
	bytes, err := json.Marshal(map[string]interface{}{
		"X-Hasura-User-Id":  sessionContainer.GetUserID(),
		"X-Hasura-Role":     "user",
		"X-Hasura-Is-Owner": "false",
	})

	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte("error in converting to json"))
	} else {
		w.Write(bytes)
	}
}
