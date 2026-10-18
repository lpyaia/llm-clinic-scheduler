// Package config loads local configuration without overriding process variables.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadEnv finds the module root from the working directory and loads its .env.
// A missing file is allowed so containers and CI can use process variables alone.
func LoadEnv() error {
	start, err := os.Getwd()
	if err != nil {
		return errors.New("não foi possível localizar o diretório de configuração")
	}

	root := start
	for {
		_, err := os.Stat(filepath.Join(root, "go.mod"))
		if err == nil {
			break
		}

		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("não foi possível localizar a raiz do projeto")
		}

		parent := filepath.Dir(root)
		if parent == root {
			root = start
			break
		}

		root = parent
	}

	err = godotenv.Load(filepath.Join(root, ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Parser errors can include secret values from the file.
		return errors.New("não foi possível carregar a .env; verifique as permissões e a sintaxe")
	}

	return nil
}
