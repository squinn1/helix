package controller

import (
	"embed"
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/helixml/helix/api/pkg/filestore"
	"github.com/helixml/helix/api/pkg/types"
)

//go:embed filestore_folders.json
var jsonFile embed.FS

func GetFolders() ([]filestore.Folder, error) {
	file, err := jsonFile.Open("filestore_folders.json")
	if err != nil {
		return []filestore.Folder{}, err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return []filestore.Folder{}, err
	}

	var folders []filestore.Folder
	if err := json.Unmarshal(content, &folders); err != nil {
		return []filestore.Folder{}, err
	}

	return folders, nil
}

func GetSessionFolder(sessionID string) string {
	return filepath.Join("sessions", sessionID)
}

func GetDataEntityFolder(ID string) string {
	return filepath.Join("data", ID)
}

func GetInteractionInputsFolder(sessionID string, interactionID string) string {
	return filepath.Join(GetSessionFolder(sessionID), "inputs", interactionID)
}

func GetSessionResultsFolder(sessionID string) string {
	return filepath.Join(GetSessionFolder(sessionID), "results")
}

func (c *Controller) GetFilestoreUserPath(ctx types.OwnerContext, path string) (string, error) {
	userPrefix := filestore.GetUserPrefix(c.Options.Config.Controller.FilePrefixGlobal, ctx.Owner)

	return filepath.Join(userPrefix, path), nil
}

func (c *Controller) VerifySignature(url string) bool {
	return filestore.VerifySignature(url, c.Options.Config.Controller.FilestorePresignSecret)
}

func (c *Controller) GetFilestoreSessionPath(ctx types.OwnerContext, sessionID string) (string, error) {
	return c.GetFilestoreUserPath(ctx, GetSessionFolder(sessionID))
}

func (c *Controller) GetFilestoreInteractionInputsPath(ctx types.OwnerContext, sessionID string, interactionID string) (string, error) {
	return c.GetFilestoreUserPath(ctx, GetInteractionInputsFolder(sessionID, interactionID))
}

func (c *Controller) GetFilestoreResultsPath(ctx types.OwnerContext, sessionID string, _ string) (string, error) {
	return c.GetFilestoreUserPath(ctx, GetSessionResultsFolder(sessionID))
}

// given a path - we might have never seen this filestore yet
// so, we must ensure that the users base path is created and then create each
// special folder as listed above
func (c *Controller) ensureFilestoreUserPath(ctx types.OwnerContext, path string) (string, error) {
	userPath, err := c.GetFilestoreUserPath(ctx, "")
	if err != nil {
		return "", err
	}
	_, err = c.Options.Filestore.CreateFolder(c.Ctx, userPath)
	if err != nil {
		return "", err
	}

	// now we loop over the top level folders and ensure they exist
	folders, err := GetFolders()
	if err != nil {
		return "", err
	}
	for _, folder := range folders {
		_, err := c.Options.Filestore.CreateFolder(c.Ctx, filepath.Join(userPath, folder.Name))
		if err != nil {
			return "", err
		}
	}
	retPath, err := c.GetFilestoreUserPath(ctx, path)
	if err != nil {
		return "", err
	}
	return retPath, nil
}

func (c *Controller) FilestoreConfig(ctx types.OwnerContext) (filestore.Config, error) {
	userPrefix := filestore.GetUserPrefix(c.Options.Config.Controller.FilePrefixGlobal, ctx.Owner)

	folders, err := GetFolders()
	if err != nil {
		return filestore.Config{}, err
	}
	return filestore.Config{
		UserPrefix: userPrefix,
		Folders:    folders,
	}, nil
}

func (c *Controller) FilestoreList(ctx types.OwnerContext, path string) ([]filestore.Item, error) {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.Options.Filestore.List(c.Ctx, filePath)
}

func (c *Controller) FilestoreGet(ctx types.OwnerContext, path string) (filestore.Item, error) {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return filestore.Item{}, err
	}
	return c.Options.Filestore.Get(c.Ctx, filePath)
}

func (c *Controller) FilestoreCreateFolder(ctx types.OwnerContext, path string) (filestore.Item, error) {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return filestore.Item{}, err
	}
	return c.Options.Filestore.CreateFolder(c.Ctx, filePath)
}

func (c *Controller) FilestoreDownloadFile(ctx types.OwnerContext, path string) (io.ReadCloser, error) {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.Options.Filestore.OpenFile(c.Ctx, filePath)
}

func (c *Controller) FilestoreDownloadFolder(ctx types.OwnerContext, path string) (io.Reader, error) {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.Options.Filestore.DownloadFolder(c.Ctx, filePath)
}

func (c *Controller) FilestoreUploadFile(ctx types.OwnerContext, path string, r io.Reader) (filestore.Item, error) {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return filestore.Item{}, err
	}
	return c.Options.Filestore.WriteFile(c.Ctx, filePath, r)
}

func (c *Controller) FilestoreRename(ctx types.OwnerContext, path string, newPath string) (filestore.Item, error) {
	fullPath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return filestore.Item{}, err
	}
	fullNewPath, err := c.ensureFilestoreUserPath(ctx, newPath)
	if err != nil {
		return filestore.Item{}, err
	}
	return c.Options.Filestore.Rename(c.Ctx, fullPath, fullNewPath)
}

func (c *Controller) FilestoreDelete(ctx types.OwnerContext, path string) error {
	filePath, err := c.ensureFilestoreUserPath(ctx, path)
	if err != nil {
		return err
	}
	return c.Options.Filestore.Delete(c.Ctx, filePath)
}
