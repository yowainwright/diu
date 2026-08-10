package main

import "github.com/yowainwright/diu/internal/storage"

type localStorageSnapshot = storage.JSONInspection

func readLocalStorage(path string) (localStorageSnapshot, error) {
	return storage.InspectJSONFile(path)
}
