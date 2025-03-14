package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/helixml/helix/api/pkg/apps"
	"github.com/helixml/helix/api/pkg/controller/knowledge"
	"github.com/helixml/helix/api/pkg/filestore"
	"github.com/helixml/helix/api/pkg/store"
	"github.com/helixml/helix/api/pkg/system"
	"github.com/helixml/helix/api/pkg/tools"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
)

// listApps godoc
// @Summary List apps
// @Description List apps for the user. Apps are pre-configured to spawn sessions with specific tools and config.
// @Tags    apps

// @Success 200 {array} types.App
// @Param organization_id query string false "Organization ID"
// @Router /api/v1/apps [get]
// @Security BearerAuth
func (s *HelixAPIServer) listApps(_ http.ResponseWriter, r *http.Request) ([]*types.App, *system.HTTPError) {
	ctx := r.Context()
	user := getRequestUser(r)
	orgID := r.URL.Query().Get("organization_id") // If filtering for a specific organization

	if orgID != "" {
		orgApps, err := s.listOrganizationApps(ctx, user, orgID)
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		orgApps = s.populateAppOwner(ctx, orgApps)

		return orgApps, nil
	}

	userApps, err := s.Store.ListApps(ctx, &store.ListAppsQuery{
		Owner:     user.ID,
		OwnerType: user.Type,
	})
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	// remove global apps from the list in case this is the admin user who created the global app
	nonGlobalUserApps := []*types.App{}
	for _, app := range userApps {
		if !app.Global {
			nonGlobalUserApps = append(nonGlobalUserApps, app)
		}
	}

	globalApps, err := s.Store.ListApps(r.Context(), &store.ListAppsQuery{
		Global: true,
	})
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	allApps := append(nonGlobalUserApps, globalApps...)

	// Filter apps based on the "type" query parameter
	var filteredApps []*types.App
	for _, app := range allApps {
		if !isAdmin(user) && app.Global {
			if app.Config.Github != nil {
				app.Config.Github.KeyPair.PrivateKey = ""
				app.Config.Github.WebhookSecret = ""
			}
		}

		filteredApps = append(filteredApps, app)
	}

	filteredApps = s.populateAppOwner(ctx, filteredApps)

	return filteredApps, nil
}

func (s *HelixAPIServer) populateAppOwner(ctx context.Context, apps []*types.App) []*types.App {
	userMap := make(map[string]*types.User)

	for _, app := range apps {
		// Populate the user map if the user is not already in the map
		if _, ok := userMap[app.Owner]; !ok {
			appOwner, err := s.Store.GetUser(ctx, &store.GetUserQuery{ID: app.Owner})
			if err != nil {
				continue
			}

			userMap[app.Owner] = appOwner
		}
	}

	// Assign the user to the app
	for _, app := range apps {
		user, ok := userMap[app.Owner]
		if !ok {
			app.User = types.User{}
			continue
		}

		app.User = *user
	}

	return apps
}

// listOrganizationApps lists apps for an organization based on the user's access grants
func (s *HelixAPIServer) listOrganizationApps(ctx context.Context, user *types.User, orgID string) ([]*types.App, *system.HTTPError) {
	orgMembership, err := s.authorizeOrgMember(ctx, user, orgID)
	if err != nil {
		return nil, system.NewHTTPError403(err.Error())
	}

	apps, err := s.Store.ListApps(ctx, &store.ListAppsQuery{
		OrganizationID: orgID,
	})
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	// If user is the org owner, skip authorization, they can view all apps
	if orgMembership.Role == types.OrganizationRoleOwner {
		return apps, nil
	}

	// User is not the org owner, so we need to authorize them to each app

	var authorizedApps []*types.App

	// Authorize the user to each app
	for _, app := range apps {
		err := s.authorizeUserToApp(ctx, user, app, types.ActionGet)
		if err != nil {
			log.Debug().
				Str("user_id", user.ID).
				Str("app_id", app.ID).
				Str("action", types.ActionGet.String()).
				Msg("user is not authorized to view app")
			continue
		}

		// Get user for the app
		appOwner, err := s.Store.GetUser(ctx, &store.GetUserQuery{ID: app.Owner})
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		app.User = *appOwner

		authorizedApps = append(authorizedApps, app)
	}

	return authorizedApps, nil
}

// createTool godoc
// @Summary Create new app
// @Description Create new app. Apps are pre-configured to spawn sessions with specific tools and config.
// @Tags    apps

// @Success 200 {object} types.App
// @Param request    body types.App true "Request body with app configuration.")
// @Router /api/v1/apps [post]
// @Security BearerAuth
func (s *HelixAPIServer) createApp(_ http.ResponseWriter, r *http.Request) (*types.App, *system.HTTPError) {
	user := getRequestUser(r)
	ctx := r.Context()

	var app types.App
	body, err := io.ReadAll(r.Body)

	if err != nil {
		return nil, system.NewHTTPError400(fmt.Sprintf("failed to read request body, error: %s", err))
	}
	err = json.Unmarshal(body, &app)
	if err != nil {
		return nil, system.NewHTTPError400(fmt.Sprintf("failed to decode request body 1, error: %s, body: %s", err, string(body)))
	}

	// If organization ID is set, authorize the user to the organization,
	// must be a member to create it
	if app.OrganizationID != "" {
		_, err := s.authorizeOrgMember(r.Context(), user, app.OrganizationID)
		if err != nil {
			return nil, system.NewHTTPError403(err.Error())
		}
	}

	// Getting existing tools for the user
	existingApps, err := s.Store.ListApps(ctx, &store.ListAppsQuery{
		Owner:     user.ID,
		OwnerType: user.Type,
	})
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	app.ID = system.GenerateAppID()
	app.Owner = user.ID
	app.OwnerType = user.Type
	app.Updated = time.Now()

	err = s.validateProviderAndModel(ctx, user, &app)
	if err != nil {
		return nil, system.NewHTTPError400(err.Error())
	}

	// if the create query param is truthy, set the name to the ID
	// this is so the frontend can quickly create the app before redirecting to the edit page for the new app
	// this avoids us "editing" a new app that does have an ID yet which causes various other problems
	// (for example adding/editing RAG sources or any other one-to-(one|many) relationships)
	if createParam := r.URL.Query().Get("create"); createParam != "" {
		app.Config.Helix.Name = app.ID
	}

	for _, a := range existingApps {
		if app.Config.Helix.Name != "" && a.Config.Helix.Name == app.Config.Helix.Name {
			return nil, system.NewHTTPError400(fmt.Sprintf("app (%s) with name %s already exists", a.ID, a.Config.Helix.Name))
		}
	}

	var created *types.App

	// if this is a github app - then initialise it
	switch app.AppSource {
	case types.AppSourceHelix:
		err = s.validateTriggers(app.Config.Helix.Triggers)
		if err != nil {
			return nil, system.NewHTTPError400(err.Error())
		}

		// Validate and default tools
		for idx := range app.Config.Helix.Assistants {
			assistant := &app.Config.Helix.Assistants[idx]
			for idx := range assistant.Tools {
				tool := assistant.Tools[idx]
				err = tools.ValidateTool(tool, s.Controller.ToolsPlanner, true)
				if err != nil {
					return nil, system.NewHTTPError400(err.Error())
				}
			}

			for _, k := range assistant.Knowledge {
				err = s.validateKnowledge(k)
				if err != nil {
					return nil, system.NewHTTPError400(err.Error())
				}
			}
		}

		created, err = s.Store.CreateApp(ctx, &app)
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		log.Info().Msgf("Created Helix (local source) app %s", created.ID)
	case types.AppSourceGithub:
		if app.Config.Github.Repo == "" {
			return nil, system.NewHTTPError400("github repo is required")
		}
		created, err = s.Store.CreateApp(ctx, &app)
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		log.Info().Msgf("Created Helix (local source) app %s", created.ID)

		client, err := s.getGithubClientFromRequest(r)
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}
		githubApp, err := apps.NewGithubApp(apps.AppOptions{
			GithubConfig: s.Cfg.GitHub,
			Client:       client,
			App:          created,
			ToolsPlanner: s.Controller.ToolsPlanner,
			UpdateApp: func(app *types.App) (*types.App, error) {
				return s.Store.UpdateApp(r.Context(), app)
			},
		})
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		newApp, err := githubApp.Create()
		if err != nil {
			if delErr := s.Store.DeleteApp(r.Context(), created.ID); delErr != nil {
				return nil, system.NewHTTPError500(fmt.Sprintf("%v: %v", delErr, err))
			}
			return nil, system.NewHTTPError500(err.Error())
		}

		created, err = s.Store.UpdateApp(r.Context(), newApp)
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}
	default:
		return nil, system.NewHTTPError400(
			fmt.Sprintf("unknown app source, available sources: %s, %s",
				types.AppSourceHelix,
				types.AppSourceGithub))
	}

	err = s.ensureKnowledge(ctx, created)
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	_, err = s.Controller.CreateAPIKey(ctx, user, &types.ApiKey{
		Name:  "api key 1",
		Type:  types.APIkeytypeApp,
		AppID: &sql.NullString{String: created.ID, Valid: true},
	})
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	return created, nil
}

// validateProviderAndModel checks if the provider and model are valid. Provider
// can be empty, however model is required
func (s *HelixAPIServer) validateProviderAndModel(ctx context.Context, user *types.User, app *types.App) error {
	if len(app.Config.Helix.Assistants) == 0 {
		return fmt.Errorf("app must have at least one assistant")
	}

	providers, err := s.providerManager.ListProviders(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("failed to list models: %w", err)
	}

	// Validate providers
	for _, assistant := range app.Config.Helix.Assistants {
		if assistant.Model == "" {
			return fmt.Errorf("assistant '%s' must have a model", assistant.Name)
		}

		// If provider set, check if we have it
		if assistant.Provider != "" {
			if !slices.Contains(providers, types.Provider(assistant.Provider)) {
				return fmt.Errorf("provider '%s' is not available", assistant.Provider)
			}
			// OK
		}
	}

	return nil
}

func (s *HelixAPIServer) validateKnowledge(k *types.AssistantKnowledge) error {
	return knowledge.Validate(s.Cfg, k)
}

func (s *HelixAPIServer) validateTriggers(triggers []types.Trigger) error {
	// If it's cron, check that it runs not more than once every 90 seconds
	for _, trigger := range triggers {
		if trigger.Cron != nil && trigger.Cron.Schedule != "" {
			cronSchedule, err := cron.ParseStandard(trigger.Cron.Schedule)
			if err != nil {
				return fmt.Errorf("invalid cron schedule: %w", err)
			}

			nextRun := cronSchedule.Next(time.Now())
			secondRun := cronSchedule.Next(nextRun)
			if secondRun.Sub(nextRun) < 90*time.Second {
				return fmt.Errorf("cron trigger must not run more than once per 90 seconds")
			}
		}
	}
	return nil
}

// ensureKnowledge creates or updates knowledge config in the database
func (s *HelixAPIServer) ensureKnowledge(ctx context.Context, app *types.App) error {
	var knowledge []*types.AssistantKnowledge

	// Get knowledge for all assistants
	for _, assistant := range app.Config.Helix.Assistants {
		knowledge = append(knowledge, assistant.Knowledge...)
	}

	// Used to track which knowledges are declared in the app config
	// so we can delete knowledges that are no longer specified
	foundKnowledge := make(map[string]bool)

	for _, k := range knowledge {
		// Scope the filestore path to the app's directory if it exists
		if k.Source.Filestore != nil && k.Source.Filestore.Path != "" {
			// Translate simple paths like "pdfs" to "apps/:app_id/pdfs"
			ownerCtx := types.OwnerContext{
				Owner:     app.Owner,
				OwnerType: app.OwnerType,
			}

			scopedPath, err := s.Controller.GetFilestoreAppKnowledgePath(ownerCtx, app.ID, k.Source.Filestore.Path)
			if err != nil {
				return fmt.Errorf("failed to generate scoped path for knowledge '%s': %w", k.Name, err)
			}

			// Remove the global prefix from the path to store only the relative path in the database
			appPrefix := filestore.GetAppPrefix(s.Cfg.Controller.FilePrefixGlobal, app.ID)
			relativePath := strings.TrimPrefix(scopedPath, appPrefix)
			relativePath = strings.TrimPrefix(relativePath, "/")

			// Update the source path to use the scoped path
			k.Source.Filestore.Path = relativePath
		}

		existing, err := s.Store.LookupKnowledge(ctx, &store.LookupKnowledgeQuery{
			AppID: app.ID,
			Name:  k.Name,
		})
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Create new knowledge
				created, err := s.Store.CreateKnowledge(ctx, &types.Knowledge{
					AppID:           app.ID,
					Name:            k.Name,
					Description:     k.Description,
					Owner:           app.Owner,
					OwnerType:       app.OwnerType,
					State:           types.KnowledgeStatePreparing,
					RAGSettings:     k.RAGSettings,
					Source:          k.Source,
					RefreshEnabled:  k.RefreshEnabled,
					RefreshSchedule: k.RefreshSchedule,
				})
				if err != nil {
					return fmt.Errorf("failed to create knowledge '%s': %w", k.Name, err)
				}
				// OK, continue
				foundKnowledge[created.ID] = true
				continue
			}
			return fmt.Errorf("failed to create knowledge '%s': %w", k.Name, err)
		}

		// Update existing knowledge
		existing.Description = k.Description
		existing.RAGSettings = k.RAGSettings
		existing.Source = k.Source
		existing.RefreshEnabled = k.RefreshEnabled
		existing.RefreshSchedule = k.RefreshSchedule

		_, err = s.Store.UpdateKnowledge(ctx, existing)
		if err != nil {
			return fmt.Errorf("failed to update knowledge '%s': %w", existing.Name, err)
		}

		foundKnowledge[existing.ID] = true
	}

	existingKnowledge, err := s.Store.ListKnowledge(ctx, &store.ListKnowledgeQuery{
		AppID: app.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to list knowledge for app '%s': %w", app.ID, err)
	}

	for _, k := range existingKnowledge {
		if !foundKnowledge[k.ID] {
			// Delete knowledge that is no longer specified in the app config
			err = s.deleteKnowledgeAndVersions(k)
			if err != nil {
				return fmt.Errorf("failed to delete knowledge '%s': %w", k.Name, err)
			}
		}
	}

	return nil
}

// what the user can change about a github app fromm the frontend
type AppUpdatePayload struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	ActiveTools    []string          `json:"active_tools"`
	Secrets        map[string]string `json:"secrets"`
	AllowedDomains []string          `json:"allowed_domains"`
	Shared         bool              `json:"shared"`
	Global         bool              `json:"global"`
}

// getApp godoc
// @Summary Get app by ID
// @Description Get app by ID.
// @Tags    apps

// @Success 200 {object} types.App
// @Param id path string true "App ID"
// @Router /api/v1/apps/{id} [get]
// @Security BearerAuth
func (s *HelixAPIServer) getApp(_ http.ResponseWriter, r *http.Request) (*types.App, *system.HTTPError) {
	user := getRequestUser(r)
	id := getID(r)

	app, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, system.NewHTTPError404(store.ErrNotFound.Error())
		}
		return nil, system.NewHTTPError500(err.Error())
	}

	if app.Global {
		return app, nil
	}

	if app.Shared {
		return app, nil
	}

	if app.OrganizationID != "" {
		err := s.authorizeUserToApp(r.Context(), user, app, types.ActionGet)
		if err != nil {
			return nil, system.NewHTTPError403(err.Error())
		}
		return app, nil
	}

	if app.Owner != user.ID {
		return nil, system.NewHTTPError404(store.ErrNotFound.Error())
	}

	return app, nil
}

// updateApp godoc
// @Summary Update an existing app
// @Description Update existing app
// @Tags    apps

// @Success 200 {object} types.App
// @Param request    body types.App true "Request body with app configuration.")
// @Param id path string true "Tool ID"
// @Router /api/v1/apps/{id} [put]
// @Security BearerAuth
func (s *HelixAPIServer) updateApp(_ http.ResponseWriter, r *http.Request) (*types.App, *system.HTTPError) {
	user := getRequestUser(r)

	var update types.App
	err := json.NewDecoder(r.Body).Decode(&update)
	if err != nil {
		return nil, system.NewHTTPError400(fmt.Sprintf("failed to decode request body 2, error: %s", err))
	}

	// Getting existing app
	existing, err := s.Store.GetApp(r.Context(), update.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, system.NewHTTPError404(store.ErrNotFound.Error())
		}
		return nil, system.NewHTTPError500(err.Error())
	}

	if existing == nil {
		return nil, system.NewHTTPError404(store.ErrNotFound.Error())
	}

	// Some fields are not allowed to be changed
	update.OrganizationID = existing.OrganizationID
	update.Owner = existing.Owner
	update.OwnerType = existing.OwnerType
	update.Created = existing.Created

	if existing.OrganizationID != "" {
		// Org mode
		err := s.authorizeUserToApp(r.Context(), user, existing, types.ActionUpdate)
		if err != nil {
			return nil, system.NewHTTPError403(err.Error())
		}
	} else {
		// Single-player mode
		if existing.Global {
			if !isAdmin(user) {
				return nil, system.NewHTTPError403("only admin users can update global apps")
			}
		} else {
			if existing.Owner != user.ID {
				return nil, system.NewHTTPError403("you do not have permission to update this app")
			}
		}
	}

	err = s.validateProviderAndModel(r.Context(), user, &update)
	if err != nil {
		return nil, system.NewHTTPError400(err.Error())
	}

	err = s.validateTriggers(update.Config.Helix.Triggers)
	if err != nil {
		return nil, system.NewHTTPError400(err.Error())
	}

	update.Updated = time.Now()

	// Validate and default tools
	for idx := range update.Config.Helix.Assistants {
		assistant := &update.Config.Helix.Assistants[idx]
		for idx := range assistant.Tools {
			tool := assistant.Tools[idx]
			err = tools.ValidateTool(tool, s.Controller.ToolsPlanner, true)
			if err != nil {
				return nil, system.NewHTTPError400(err.Error())
			}
		}

		for _, k := range assistant.Knowledge {
			err = s.validateKnowledge(k)
			if err != nil {
				return nil, system.NewHTTPError400(err.Error())
			}
		}
	}

	// Updating the app
	updated, err := s.Store.UpdateApp(r.Context(), &update)
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	err = s.ensureKnowledge(r.Context(), updated)
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	return updated, nil
}

// updateGithubApp godoc
// @Summary Update an existing app
// @Description Update existing app
// @Tags    apps

// @Success 200 {object} types.App
// @Param request    body types.App true "Request body with app configuration.")
// @Param id path string true "Tool ID"
// @Router /api/v1/apps/github/{id} [put]
// @Security BearerAuth
func (s *HelixAPIServer) updateGithubApp(_ http.ResponseWriter, r *http.Request) (*types.App, *system.HTTPError) {
	user := getRequestUser(r)

	var appUpdate AppUpdatePayload
	err := json.NewDecoder(r.Body).Decode(&appUpdate)
	if err != nil {
		return nil, system.NewHTTPError400(fmt.Sprintf("failed to decode request body 3, error: %s", err))
	}

	if appUpdate.ActiveTools == nil {
		appUpdate.ActiveTools = []string{}
	}

	if appUpdate.AllowedDomains == nil {
		appUpdate.AllowedDomains = []string{}
	}

	if appUpdate.Secrets == nil {
		appUpdate.Secrets = map[string]string{}
	}

	id := getID(r)

	// Getting existing app
	existing, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, system.NewHTTPError404(store.ErrNotFound.Error())
		}
		return nil, system.NewHTTPError500(err.Error())
	}

	if existing == nil {
		return nil, system.NewHTTPError404(store.ErrNotFound.Error())
	}

	if appUpdate.Global {
		if !isAdmin(user) {
			return nil, system.NewHTTPError403("only admin users can update global apps")
		}
	} else {
		if existing.Owner != user.ID {
			return nil, system.NewHTTPError403("you do not have permission to update this app")
		}
	}

	if existing.AppSource == types.AppSourceGithub {
		client, err := s.getGithubClientFromRequest(r)
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}
		githubApp, err := apps.NewGithubApp(apps.AppOptions{
			GithubConfig: s.Cfg.GitHub,
			Client:       client,
			App:          existing,
			ToolsPlanner: s.Controller.ToolsPlanner,
			UpdateApp: func(app *types.App) (*types.App, error) {
				return s.Store.UpdateApp(r.Context(), app)
			},
		})
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		existing, err = githubApp.Update()
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}
	}

	existing.Updated = time.Now()
	existing.Config.Secrets = appUpdate.Secrets
	existing.Config.AllowedDomains = appUpdate.AllowedDomains
	existing.Shared = appUpdate.Shared
	existing.Global = appUpdate.Global

	// Updating the app
	updated, err := s.Store.UpdateApp(r.Context(), existing)
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	return updated, nil
}

// deleteApp godoc
// @Summary Delete app
// @Description Delete app.
// @Tags    apps

// @Success 200
// @Param id path string true "App ID"
// @Router /api/v1/apps/{id} [delete]
// @Security BearerAuth
func (s *HelixAPIServer) deleteApp(_ http.ResponseWriter, r *http.Request) (*types.App, *system.HTTPError) {
	user := getRequestUser(r)
	id := getID(r)

	// Users need to specify if they want to keep the knowledge, by default - deleting
	keepKnowledge := r.URL.Query().Get("keep_knowledge") == "true"

	existing, err := s.Store.GetApp(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, system.NewHTTPError404(store.ErrNotFound.Error())
		}
		return nil, system.NewHTTPError500(err.Error())
	}

	if existing.OrganizationID != "" {
		// Org mode
		err := s.authorizeUserToApp(r.Context(), user, existing, types.ActionDelete)
		if err != nil {
			return nil, system.NewHTTPError403(err.Error())
		}

	} else {
		if existing.Global {
			if !isAdmin(user) {
				return nil, system.NewHTTPError403("only admin users can delete global apps")
			}
		} else {
			if existing.Owner != user.ID {
				return nil, system.NewHTTPError403("you do not have permission to delete this app")
			}
		}
	}

	if !keepKnowledge {
		knowledge, err := s.Store.ListKnowledge(r.Context(), &store.ListKnowledgeQuery{
			AppID: id,
		})
		if err != nil {
			return nil, system.NewHTTPError500(err.Error())
		}

		for _, k := range knowledge {
			err = s.Store.DeleteKnowledge(r.Context(), k.ID)
			if err != nil {
				return nil, system.NewHTTPError500(err.Error())
			}
		}
	}

	err = s.Store.DeleteApp(r.Context(), id)
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	return existing, nil
}

// appRunScript godoc
// @Summary Run a GPT script inside a github app
// @Description Run a GPT script inside a github app.
// @Tags    apps

// @Success 200 {object} types.GptScriptResponse
// @Param request    body types.GptScriptRequest true "Request body with script configuration.")
// @Router /api/v1/apps/{id}/gptscript [post]
// @Security BearerAuth
func (s *HelixAPIServer) appRunScript(w http.ResponseWriter, r *http.Request) (*types.GptScriptResponse, *system.HTTPError) {
	// TODO: authenticate the referer based on app settings
	addCorsHeaders(w)
	if r.Method == "OPTIONS" {
		return nil, nil
	}
	user := getRequestUser(r)

	if user.AppID == "" {
		return nil, system.NewHTTPError403("no api key for app found")
	}

	appRecord, err := s.Store.GetApp(r.Context(), user.AppID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, system.NewHTTPError404("app not found")
		}
		return nil, system.NewHTTPError500(err.Error())
	}

	// load the body of the request
	var req types.GptScriptRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return nil, system.NewHTTPError400(fmt.Sprintf("failed to decode request body 4, error: %s", err))
	}

	envPairs := []string{}
	for key, value := range appRecord.Config.Secrets {
		envPairs = append(envPairs, key+"="+value)
	}

	logger := log.With().
		Str("app_id", user.AppID).
		Str("user_id", user.ID).Logger()

	logger.Trace().Msg("starting app execution")

	app := &types.GptScriptGithubApp{
		Script: types.GptScript{
			FilePath: req.FilePath,
			Input:    req.Input,
			Env:      envPairs,
		},
		Repo:       appRecord.Config.Github.Repo,
		CommitHash: appRecord.Config.Github.Hash,
		KeyPair:    appRecord.Config.Github.KeyPair,
	}

	start := time.Now()

	result, err := s.gptScriptExecutor.ExecuteApp(r.Context(), app)
	if err != nil {

		logger.Warn().Err(err).Str("duration", time.Since(start).String()).Msg("app execution failed")

		// Log error
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, runErr := s.Store.CreateScriptRun(ctx, &types.ScriptRun{
			Owner:       user.ID,
			OwnerType:   user.Type,
			AppID:       user.AppID,
			State:       types.ScriptRunStateError,
			Type:        types.GptScriptRunnerTaskTypeGithubApp,
			SystemError: err.Error(),
			DurationMs:  int(time.Since(start).Milliseconds()),
			Request: &types.GptScriptRunnerRequest{
				GithubApp: app,
			},
		})
		if runErr != nil {
			log.Err(runErr).Msg("failed to create script run")
		}

		return nil, system.NewHTTPError500(err.Error())
	}

	logger.Info().Str("duration", time.Since(start).String()).Msg("app executed")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = s.Store.CreateScriptRun(ctx, &types.ScriptRun{
		Owner:      user.ID,
		OwnerType:  user.Type,
		AppID:      user.AppID,
		State:      types.ScriptRunStateComplete,
		Type:       types.GptScriptRunnerTaskTypeGithubApp,
		Retries:    result.Retries,
		DurationMs: int(time.Since(start).Milliseconds()),
		Request: &types.GptScriptRunnerRequest{
			GithubApp: app,
		},
		Response: result,
	})
	if err != nil {
		log.Err(err).Msg("failed to create script run")
	}

	return result, nil
}

// appRunAPIAction godoc
// @Summary Run an API action with parameters
// @Description Run an API action with parameters
// @Tags    apps

// @Success 200 {object} types.RunAPIActionResponse
// @Param request    body types.RunAPIActionRequest true "Request body with API action name and parameters.")
// @Router /api/v1/apps/{id}/api-actions [post]
// @Security BearerAuth
func (s *HelixAPIServer) appRunAPIAction(_ http.ResponseWriter, r *http.Request) (*types.RunAPIActionResponse, *system.HTTPError) {
	user := getRequestUser(r)
	id := getID(r)

	app, err := s.Store.GetAppWithTools(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, system.NewHTTPError404("app not found")
		}
		return nil, system.NewHTTPError500(err.Error())
	}

	if user.ID != app.Owner && !app.Global {
		return nil, system.NewHTTPError403("you do not have permission to run this action")
	}

	// load the body of the request
	var req types.RunAPIActionRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return nil, system.NewHTTPError400(fmt.Sprintf("failed to decode request body 4, error: %s", err))
	}

	if req.Action == "" {
		return nil, system.NewHTTPError400("action is required")
	}

	if len(app.Config.Helix.Assistants) == 0 {
		return nil, system.NewHTTPError400("action is required")
	}

	// Validate whether action is valid

	tool, ok := tools.GetToolFromAction(app.Config.Helix.Assistants[0].Tools, req.Action)
	if !ok {
		return nil, system.NewHTTPError400(fmt.Sprintf("action %s not found in the assistant tools", req.Action))
	}

	req.Tool = tool

	response, err := s.Controller.ToolsPlanner.RunAPIActionWithParameters(r.Context(), &req)
	if err != nil {
		return nil, system.NewHTTPError500(err.Error())
	}

	return response, nil
}
