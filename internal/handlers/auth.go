package handlers

import (
	"net/http"
	"time"

	"github.com/felan/blindsidian/internal/database"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "blindsidian_session"
	sessionDuration   = 24 * time.Hour
)

func (s *Server) currentUserID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	userID, ok := s.SessionStore.GetUserID(cookie.Value)
	return userID, ok
}

func (s *Server) SignupHandler(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if r.Method == http.MethodPost {
		email := r.FormValue("email")
		password := r.FormValue("password")
		if email == "" || password == "" {
			errMsg = "Email and password are required."
		} else {
			existingUser, err := s.DBManager.GetUserByEmail(s.SystemDB, email)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if existingUser != nil {
				errMsg = "Email is already registered."
			} else {
				uid := uuid.NewString()
				hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
				if err != nil {
					http.Error(w, "unable to hash password", http.StatusInternalServerError)
					return
				}

				if err := s.DBManager.CreateSystemUser(s.SystemDB, database.User{ID: uid, Email: email, PasswordHash: string(hash)}); err != nil {
					http.Error(w, "unable to create user", http.StatusInternalServerError)
					return
				}

				if _, err := s.DBManager.CreateUserDB(uid); err != nil {
					http.Error(w, "unable to initialize user storage", http.StatusInternalServerError)
					return
				}

				token, err := s.SessionStore.CreateSession(uid)
				if err != nil {
					http.Error(w, "unable to create session", http.StatusInternalServerError)
					return
				}

				http.SetCookie(w, &http.Cookie{
					Name:     sessionCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
					Secure:   false,
					Expires:  time.Now().Add(sessionDuration),
				})
				http.Redirect(w, r, "/notes", http.StatusSeeOther)
				return
			}
		}
	}

	s.RenderTemplate(w, "signup.gohtml", map[string]string{"Error": errMsg})
}

func (s *Server) LoginHandler(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	if r.Method == http.MethodPost {
		email := r.FormValue("email")
		password := r.FormValue("password")
		if email == "" || password == "" {
			errMsg = "Email and password are required."
		} else {
			user, err := s.DBManager.GetUserByEmail(s.SystemDB, email)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if user == nil {
				errMsg = "Invalid email or password."
			} else if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
				errMsg = "Invalid email or password."
			} else {
				token, err := s.SessionStore.CreateSession(user.ID)
				if err != nil {
					http.Error(w, "unable to create session", http.StatusInternalServerError)
					return
				}
				http.SetCookie(w, &http.Cookie{
					Name:     sessionCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
					Secure:   false,
					Expires:  time.Now().Add(sessionDuration),
				})
				http.Redirect(w, r, "/notes", http.StatusSeeOther)
				return
			}
		}
	}
	s.RenderTemplate(w, "login.gohtml", map[string]string{"Error": errMsg})
}

func (s *Server) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		s.SessionStore.DeleteSession(cookie.Value)
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1})
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
