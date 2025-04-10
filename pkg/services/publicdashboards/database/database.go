package database

import (
	"context"
	"encoding/json"

	"github.com/grafana/grafana/pkg/infra/db"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/dashboards"
	"github.com/grafana/grafana/pkg/services/publicdashboards"
	. "github.com/grafana/grafana/pkg/services/publicdashboards/models"
)

// Define the storage implementation. We're generating the mock implementation
// automatically
type PublicDashboardStoreImpl struct {
	sqlStore db.DB
	log      log.Logger
}

var LogPrefix = "publicdashboards.store"

// Gives us a compile time error if our database does not adhere to contract of
// the interface
var _ publicdashboards.Store = (*PublicDashboardStoreImpl)(nil)

// Factory used by wire to dependency injection
func ProvideStore(sqlStore db.DB) *PublicDashboardStoreImpl {
	return &PublicDashboardStoreImpl{
		sqlStore: sqlStore,
		log:      log.New(LogPrefix),
	}
}

// FindAll Returns a list of public dashboards by orgId
func (d *PublicDashboardStoreImpl) FindAll(ctx context.Context, orgId int64) ([]PublicDashboardListResponse, error) {
	resp := make([]PublicDashboardListResponse, 0)

	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		sess.Table("dashboard_public").
			Join("LEFT", "dashboard", "dashboard.uid = dashboard_public.dashboard_uid AND dashboard.org_id = dashboard_public.org_id").
			Cols("dashboard_public.uid", "dashboard_public.access_token", "dashboard_public.dashboard_uid", "dashboard_public.is_enabled", "dashboard.title").
			Where("dashboard_public.org_id = ?", orgId).
			OrderBy(" is_enabled DESC, dashboard.title IS NULL, dashboard.title ASC")

		err := sess.Find(&resp)
		return err
	})

	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (d *PublicDashboardStoreImpl) FindDashboard(ctx context.Context, dashboardUid string, orgId int64) (*models.Dashboard, error) {
	dashboard := &models.Dashboard{Uid: dashboardUid, OrgId: orgId}
	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		has, err := sess.Get(dashboard)
		if err != nil {
			return err
		}
		if !has {
			return ErrPublicDashboardNotFound
		}
		return nil
	})

	return dashboard, err
}

// Find Returns public dashboard configuration by Uid or nil if not found
func (d *PublicDashboardStoreImpl) Find(ctx context.Context, uid string) (*PublicDashboard, error) {
	if uid == "" {
		return nil, nil
	}

	var found bool
	pdRes := &PublicDashboard{Uid: uid}
	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		var err error
		found, err = sess.Get(pdRes)
		return err
	})

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, nil
	}

	return pdRes, err
}

// FindByAccessToken Returns public dashboard by access token or nil if not found
func (d *PublicDashboardStoreImpl) FindByAccessToken(ctx context.Context, accessToken string) (*PublicDashboard, error) {
	if accessToken == "" {
		return nil, ErrPublicDashboardIdentifierNotSet
	}

	var found bool
	pdRes := &PublicDashboard{AccessToken: accessToken}
	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		var err error
		found, err = sess.Get(pdRes)
		return err
	})

	if err != nil {
		return nil, err
	}

	if !found {
		return nil, nil
	}

	return pdRes, err
}

// FindByDashboardUid Retrieves public dashboard configuration by dashboard uid
func (d *PublicDashboardStoreImpl) FindByDashboardUid(ctx context.Context, orgId int64, dashboardUid string) (*PublicDashboard, error) {
	if dashboardUid == "" {
		return nil, dashboards.ErrDashboardIdentifierNotSet
	}

	pdRes := &PublicDashboard{OrgId: orgId, DashboardUid: dashboardUid}
	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		// publicDashboard
		exists, err := sess.Get(pdRes)
		if err != nil {
			return err
		}
		if !exists {
			return ErrPublicDashboardNotFound
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return pdRes, err
}

// Save Persists public dashboard configuration
func (d *PublicDashboardStoreImpl) Save(ctx context.Context, cmd SavePublicDashboardConfigCommand) error {
	if cmd.PublicDashboard.DashboardUid == "" {
		return dashboards.ErrDashboardIdentifierNotSet
	}

	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		_, err := sess.UseBool("is_enabled").Insert(&cmd.PublicDashboard)
		if err != nil {
			return err
		}

		return nil
	})

	return err
}

// Update updates existing public dashboard configuration
func (d *PublicDashboardStoreImpl) Update(ctx context.Context, cmd SavePublicDashboardConfigCommand) error {
	err := d.sqlStore.WithTransactionalDbSession(ctx, func(sess *db.Session) error {
		timeSettingsJSON, err := json.Marshal(cmd.PublicDashboard.TimeSettings)
		if err != nil {
			return err
		}

		_, err = sess.Exec("UPDATE dashboard_public SET is_enabled = ?, annotations_enabled = ?, time_settings = ?, updated_by = ?, updated_at = ? WHERE uid = ?",
			cmd.PublicDashboard.IsEnabled,
			cmd.PublicDashboard.AnnotationsEnabled,
			string(timeSettingsJSON),
			cmd.PublicDashboard.UpdatedBy,
			cmd.PublicDashboard.UpdatedAt.UTC().Format("2006-01-02 15:04:05"),
			cmd.PublicDashboard.Uid)

		if err != nil {
			return err
		}

		return nil
	})

	return err
}

// ExistsEnabledByDashboardUid Responds true if there is an enabled public dashboard for a dashboard uid
func (d *PublicDashboardStoreImpl) ExistsEnabledByDashboardUid(ctx context.Context, dashboardUid string) (bool, error) {
	hasPublicDashboard := false
	err := d.sqlStore.WithDbSession(ctx, func(dbSession *db.Session) error {
		sql := "SELECT COUNT(*) FROM dashboard_public WHERE dashboard_uid=? AND is_enabled=true"

		result, err := dbSession.SQL(sql, dashboardUid).Count()
		if err != nil {
			return err
		}

		hasPublicDashboard = result > 0
		return err
	})

	return hasPublicDashboard, err
}

// ExistsEnabledByAccessToken Responds true if the accessToken exists and the public dashboard is enabled
func (d *PublicDashboardStoreImpl) ExistsEnabledByAccessToken(ctx context.Context, accessToken string) (bool, error) {
	hasPublicDashboard := false
	err := d.sqlStore.WithDbSession(ctx, func(dbSession *db.Session) error {
		sql := "SELECT COUNT(*) FROM dashboard_public WHERE access_token=? AND is_enabled=true"

		result, err := dbSession.SQL(sql, accessToken).Count()
		if err != nil {
			return err
		}

		hasPublicDashboard = result > 0
		return err
	})

	return hasPublicDashboard, err
}

// GetOrgIdByAccessToken Returns the public dashboard OrgId if exists and is enabled.
func (d *PublicDashboardStoreImpl) GetOrgIdByAccessToken(ctx context.Context, accessToken string) (int64, error) {
	var orgId int64
	err := d.sqlStore.WithDbSession(ctx, func(dbSession *db.Session) error {
		sql := "SELECT org_id FROM dashboard_public WHERE access_token=? AND is_enabled=true"

		_, err := dbSession.SQL(sql, accessToken).Get(&orgId)
		if err != nil {
			return err
		}

		return err
	})

	return orgId, err
}
