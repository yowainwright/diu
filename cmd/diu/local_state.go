package main

import "github.com/yowainwright/diu/internal/storage"

type localStorageSnapshot = storage.JSONInspection

func readLocalStorage(path string) (localStorageSnapshot, error) {
	snapshot, err := storage.InspectJSONFile(path)
	return snapshot, err
}
