package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/caarlos0/env/v6"
	"github.com/kiddikn/supertokens-with-hasura/domain"
	"github.com/supertokens/supertokens-golang/recipe/dashboard"
	"github.com/supertokens/supertokens-golang/recipe/emailpassword"
	"github.com/supertokens/supertokens-golang/recipe/emailpassword/epmodels"
	"github.com/supertokens/supertokens-golang/recipe/session"
	"github.com/supertokens/supertokens-golang/recipe/session/sessmodels"
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
	CookieDomain      string `env:"COOKIE_DOMAIN,required,notEmpty"`
	FakePassword      string `env:"FAKE_PASSWORD,required,notEmpty"`
}

var cfg config

func main() {
	cfg = config{}
	if err := env.Parse(&cfg); err != nil {
		log.Fatal(err)
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	d := domain.NewClient(cfg.HasuraAdminSecret, cfg.HasuraEndPoint)

	samesite := "none"
	cookieSecure := true
	cookieDomain := cfg.CookieDomain
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
							originalImplementation.SignUpPOST = nil
							return originalImplementation
						},
						Functions: func(originalImplementation epmodels.RecipeInterface) epmodels.RecipeInterface {
							ogResetPasswordUsingToken := *originalImplementation.ResetPasswordUsingToken
							ogSignIn := *originalImplementation.SignIn
							ogUpdateEmailOrPassword := *originalImplementation.UpdateEmailOrPassword

							(*originalImplementation.UpdateEmailOrPassword) = func(userId string, email, password *string, userContext supertokens.UserContext) (epmodels.UpdateEmailOrPasswordResponse, error) {
								if password != nil && *password == cfg.FakePassword {
									return epmodels.UpdateEmailOrPasswordResponse{}, errors.New("use a different password")
								}
								return ogUpdateEmailOrPassword(userId, email, password, userContext)
							}

							(*originalImplementation.ResetPasswordUsingToken) = func(token, newPassword string, userContext supertokens.UserContext) (epmodels.ResetPasswordUsingTokenResponse, error) {
								if newPassword == cfg.FakePassword {
									return epmodels.ResetPasswordUsingTokenResponse{
										ResetPasswordInvalidTokenError: &struct{}{},
									}, nil
								}
								return ogResetPasswordUsingToken(token, newPassword, userContext)
							}

							(*originalImplementation.SignIn) = func(email, password string, userContext supertokens.UserContext) (epmodels.SignInResponse, error) {
								if password == cfg.FakePassword {
									return epmodels.SignInResponse{
										WrongCredentialsError: &struct{}{},
									}, nil
								}
								return ogSignIn(email, password, userContext)
							}

							return originalImplementation
						},
					},
				},
			),
			dashboard.Init(nil),
			session.Init(&sessmodels.TypeInput{
				CookieSameSite: &samesite,
				CookieSecure:   &cookieSecure,
				CookieDomain:   &cookieDomain,
			}),
		},
	}); err != nil {
		return err
	}

	s, err := httpServer(cfg.Port, cfg.WebSiteDomain, cfg.HasuraEndPoint, d)
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

func httpServer(httpPort int, webSiteDomain, hasuraEndPoint string, domain *domain.Hasura) (*http.Server, error) {
	httpEndpoint := fmt.Sprintf(":%d", httpPort)
	return &http.Server{
		Addr: httpEndpoint,
		Handler: corsMiddleware(
			supertokens.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/verify" {
					session.VerifySession(nil, sessioninfo(domain)).ServeHTTP(rw, r)
					return
				}

				if r.URL.Path == "/create" {
					sessionRequired := true
					session.VerifySession(&sessmodels.VerifySessionOptions{
						SessionRequired: &sessionRequired, // NOTE:指定したグループに対してowner権限を持っているか見ないといけないのでログイン済みかどうかだけここでチェック
					}, createUserAPI).ServeHTTP(rw, r)
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
		for _, a := range []string{webSiteDomain, hasuraEndPoint} {
			if host == a {
				return host
			}
		}
		return ""
	}

	return http.HandlerFunc(func(response http.ResponseWriter, r *http.Request) {
		response.Header().Set("Access-Control-Allow-Origin", allowHost(r.Header.Get("Origin")))
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

func sessioninfo(d *domain.Hasura) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionContainer := session.GetSessionFromRequestContext(r.Context())
		if sessionContainer == nil {
			fmt.Println("no session container")
			w.WriteHeader(500)
			w.Write([]byte("no session found"))
			return
		}

		w.WriteHeader(200)
		w.Header().Add("content-type", "application/json")

		userID := sessionContainer.GetUserID()
		ur, err := d.GetUser(userID)
		if err != nil {
			fmt.Println(err)
			return
		}

		bytes, err := json.Marshal(map[string]interface{}{
			"X-Hasura-User-Id":  userID,
			"X-Hasura-Role":     domain.GetHasuraRole(ur),
			"X-Hasura-Is-Owner": "false",
		})

		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte("error in converting to json"))
		} else {
			w.Write(bytes)
		}
	}
}

func createUserAPI(w http.ResponseWriter, r *http.Request) {
	sessionContainer := session.GetSessionFromRequestContext(r.Context())
	if sessionContainer == nil {
		fmt.Println("no session container")
		w.WriteHeader(500)
		w.Write([]byte("no session found"))
		return
	}

	type Param struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	var param Param
	if err := json.NewDecoder(r.Body).Decode(&param); err != nil {
		log.Fatal(err)
		w.WriteHeader(400)
		w.Write([]byte("request decode failed"))
	}

	if param.Email == "" || param.Name == "" {
		w.WriteHeader(400)
		w.Write([]byte("request param is invalid"))
	}

	reqUserID := sessionContainer.GetUserID()
	fmt.Println(reqUserID)
	// // signUpResult, err := emailpassword.SignUp(email, cfg.FakePassword)
	// // if err != nil {
	// // 	// TODO: send 500 to the client
	// // 	return
	// // }

	// // if signUpResult.EmailAlreadyExistsError != nil {
	// // 	// TODO: send 400 to the client
	// // 	return
	// // }

	// // // we successfully created the user. Now we should send them their invite link
	// // passwordResetToken, err := emailpassword.CreateResetPasswordToken(signUpResult.OK.User.ID)
	// // if err != nil {
	// // 	// TODO: send 500 to the client
	// // 	return
	// // }

	// // inviteLink := "http://localhost:3000/auth/reset-password?token=" + passwordResetToken.OK.Token
	// // err = emailpassword.SendEmail(emaildelivery.EmailType{
	// // 	PasswordReset: &emaildelivery.PasswordResetType{
	// // 		User: emaildelivery.User{
	// // 			ID:    signUpResult.OK.User.ID,
	// // 			Email: signUpResult.OK.User.Email,
	// // 		},
	// // 		PasswordResetLink: inviteLink,
	// // 	},
	// // })
	// // if err != nil {
	// // 	// TODO: send 500 to the client
	// // 	return
	// // }
	// // // TODO: send 200 to the client

	// ↓元々signUpで呼ばれていた
	// // First we copy the original implementation
	// originalSignUpPOST := *originalImplementation.SignUpPOST

	// *originalImplementation.SignUpPOST = func(formFields []epmodels.TypeFormField, options epmodels.APIOptions, userContext supertokens.UserContext) (epmodels.SignUpPOSTResponse, error) {
	// 	resp, err := originalSignUpPOST(formFields, options, userContext)
	// 	if err != nil {
	// 		return epmodels.SignUpPOSTResponse{}, err
	// 	}

	// 	if resp.OK != nil {
	// 		// sign up was successful
	// 		id := resp.OK.User.ID
	// 		email := resp.OK.User.Email
	// 		var name string
	// 		for _, ff := range formFields {
	// 			if ff.ID == "name" {
	// 				name = ff.Value
	// 				break
	// 			}
	// 		}

	// 		if err := d.CreateUser(id, name, email); err != nil {
	// 			return epmodels.SignUpPOSTResponse{}, err
	// 		}
	// 	}

	// 	return resp, err
	// }
}
