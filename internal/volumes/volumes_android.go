package volumes

// List returns the volumes the app last reported (see SetAndroid).
func List() ([]Volume, error) { return pushed(), nil }
