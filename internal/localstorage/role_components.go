package localstorage

func BackendUploads(app *Application) *BackendUploadApplication {
	if app == nil {
		return nil
	}
	return &BackendUploadApplication{app: app}
}

func Inspect(app *Application) *InspectApplication {
	if app == nil {
		return nil
	}
	return &InspectApplication{app: app}
}

func Repair(app *Application) *RepairApplication {
	if app == nil {
		return nil
	}
	return &RepairApplication{app: app}
}

func Members(app *Application) *MemberApplication {
	if app == nil {
		return nil
	}
	return &MemberApplication{app: app}
}

func DisasterRecovery(app *Application) *DisasterRecoveryApplication {
	if app == nil {
		return nil
	}
	return &DisasterRecoveryApplication{app: app}
}

func Health(app *Application) *HealthApplication {
	if app == nil {
		return nil
	}
	return &HealthApplication{app: app}
}

type BackendUploadApplication struct {
	app *Application
}

type InspectApplication struct {
	app *Application
}

type RepairApplication struct {
	app *Application
}

type MemberApplication struct {
	app *Application
}

type DisasterRecoveryApplication struct {
	app *Application
}

type HealthApplication struct {
	app *Application
}
