package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/infra/db"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	"github.com/grafana/grafana/pkg/services/dashboards"
	dashboardsDB "github.com/grafana/grafana/pkg/services/dashboards/database"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	. "github.com/grafana/grafana/pkg/services/publicdashboards"
	"github.com/grafana/grafana/pkg/services/publicdashboards/database"
	"github.com/grafana/grafana/pkg/services/publicdashboards/internal/tokens"
	. "github.com/grafana/grafana/pkg/services/publicdashboards/models"
	"github.com/grafana/grafana/pkg/services/serviceaccounts/tests"
	"github.com/grafana/grafana/pkg/services/tag/tagimpl"
	"github.com/grafana/grafana/pkg/services/user"
	"github.com/grafana/grafana/pkg/tsdb/intervalv2"
	"github.com/grafana/grafana/pkg/util"
)

var timeSettings = &TimeSettings{From: "now-12h", To: "now"}
var defaultPubdashTimeSettings = &TimeSettings{}
var dashboardData = simplejson.NewFromAny(map[string]interface{}{"time": map[string]interface{}{"from": "now-8h", "to": "now"}})
var SignedInUser = &user.SignedInUser{UserID: 1234, Login: "user@login.com"}

func TestLogPrefix(t *testing.T) {
	assert.Equal(t, LogPrefix, "publicdashboards.service")
}

func TestGetPublicDashboard(t *testing.T) {
	type storeResp struct {
		pd  *PublicDashboard
		d   *models.Dashboard
		err error
	}

	testCases := []struct {
		Name        string
		AccessToken string
		StoreResp   *storeResp
		ErrResp     error
		DashResp    *models.Dashboard
	}{
		{
			Name:        "returns a dashboard",
			AccessToken: "abc123",
			StoreResp: &storeResp{
				pd:  &PublicDashboard{AccessToken: "abcdToken", IsEnabled: true},
				d:   &models.Dashboard{Uid: "mydashboard", Data: dashboardData},
				err: nil,
			},
			ErrResp:  nil,
			DashResp: &models.Dashboard{Uid: "mydashboard", Data: dashboardData},
		},
		{
			Name:        "returns ErrPublicDashboardNotFound when isEnabled is false",
			AccessToken: "abc123",
			StoreResp: &storeResp{
				pd:  &PublicDashboard{AccessToken: "abcdToken", IsEnabled: false},
				d:   &models.Dashboard{Uid: "mydashboard"},
				err: nil,
			},
			ErrResp:  ErrPublicDashboardNotFound,
			DashResp: nil,
		},
		{
			Name:        "returns ErrPublicDashboardNotFound if PublicDashboard missing",
			AccessToken: "abc123",
			StoreResp:   &storeResp{pd: nil, d: nil, err: nil},
			ErrResp:     ErrPublicDashboardNotFound,
			DashResp:    nil,
		},
		{
			Name:        "returns ErrPublicDashboardNotFound if Dashboard missing",
			AccessToken: "abc123",
			StoreResp:   &storeResp{pd: nil, d: nil, err: nil},
			ErrResp:     ErrPublicDashboardNotFound,
			DashResp:    nil,
		},
	}

	for _, test := range testCases {
		t.Run(test.Name, func(t *testing.T) {
			fakeStore := FakePublicDashboardStore{}
			service := &PublicDashboardServiceImpl{
				log:   log.New("test.logger"),
				store: &fakeStore,
			}

			fakeStore.On("FindByAccessToken", mock.Anything, mock.Anything).Return(test.StoreResp.pd, test.StoreResp.err)
			fakeStore.On("FindDashboard", mock.Anything, mock.Anything, mock.Anything).Return(test.StoreResp.d, test.StoreResp.err)

			pdc, dash, err := service.FindPublicDashboardAndDashboardByAccessToken(context.Background(), test.AccessToken)
			if test.ErrResp != nil {
				assert.Error(t, test.ErrResp, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, test.DashResp, dash)

			if test.DashResp != nil {
				assert.NotNil(t, dash.CreatedBy)
				assert.Equal(t, test.StoreResp.pd, pdc)
			}
		})
	}
}

func TestSavePublicDashboard(t *testing.T) {
	t.Run("Saving public dashboard", func(t *testing.T) {
		sqlStore := db.InitTestDB(t)
		dashboardStore := dashboardsDB.ProvideDashboardStore(sqlStore, sqlStore.Cfg, featuremgmt.WithFeatures(), tagimpl.ProvideService(sqlStore, sqlStore.Cfg))
		publicdashboardStore := database.ProvideStore(sqlStore)
		dashboard := insertTestDashboard(t, dashboardStore, "testDashie", 1, 0, true, []map[string]interface{}{}, nil)

		service := &PublicDashboardServiceImpl{
			log:   log.New("test.logger"),
			store: publicdashboardStore,
		}

		dto := &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       7,
			PublicDashboard: &PublicDashboard{
				IsEnabled:          true,
				AnnotationsEnabled: false,
				DashboardUid:       "NOTTHESAME",
				OrgId:              9999999,
				TimeSettings:       timeSettings,
			},
		}

		_, err := service.Save(context.Background(), SignedInUser, dto)
		require.NoError(t, err)

		pubdash, err := service.FindByDashboardUid(context.Background(), dashboard.OrgId, dashboard.Uid)
		require.NoError(t, err)

		// DashboardUid/OrgId/CreatedBy set by the command, not parameters
		assert.Equal(t, dashboard.Uid, pubdash.DashboardUid)
		assert.Equal(t, dashboard.OrgId, pubdash.OrgId)
		assert.Equal(t, dto.UserId, pubdash.CreatedBy)
		assert.Equal(t, dto.PublicDashboard.AnnotationsEnabled, pubdash.AnnotationsEnabled)
		// ExistsEnabledByDashboardUid set by parameters
		assert.Equal(t, dto.PublicDashboard.IsEnabled, pubdash.IsEnabled)
		// CreatedAt set to non-zero time
		assert.NotEqual(t, &time.Time{}, pubdash.CreatedAt)
		// Time settings set by db
		assert.Equal(t, timeSettings, pubdash.TimeSettings)
		// accessToken is valid uuid
		_, err = uuid.Parse(pubdash.AccessToken)
		require.NoError(t, err, "expected a valid UUID, got %s", pubdash.AccessToken)
	})

	t.Run("Validate pubdash has default time setting value", func(t *testing.T) {
		sqlStore := db.InitTestDB(t)
		dashboardStore := dashboardsDB.ProvideDashboardStore(sqlStore, sqlStore.Cfg, featuremgmt.WithFeatures(), tagimpl.ProvideService(sqlStore, sqlStore.Cfg))
		publicdashboardStore := database.ProvideStore(sqlStore)
		dashboard := insertTestDashboard(t, dashboardStore, "testDashie", 1, 0, true, []map[string]interface{}{}, nil)

		service := &PublicDashboardServiceImpl{
			log:   log.New("test.logger"),
			store: publicdashboardStore,
		}

		dto := &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       7,
			PublicDashboard: &PublicDashboard{
				IsEnabled:    true,
				DashboardUid: "NOTTHESAME",
				OrgId:        9999999,
			},
		}

		_, err := service.Save(context.Background(), SignedInUser, dto)
		require.NoError(t, err)

		pubdash, err := service.FindByDashboardUid(context.Background(), dashboard.OrgId, dashboard.Uid)
		require.NoError(t, err)
		assert.Equal(t, defaultPubdashTimeSettings, pubdash.TimeSettings)
	})

	t.Run("Validate pubdash whose dashboard has template variables returns error", func(t *testing.T) {
		sqlStore := db.InitTestDB(t)
		dashboardStore := dashboardsDB.ProvideDashboardStore(sqlStore, sqlStore.Cfg, featuremgmt.WithFeatures(), tagimpl.ProvideService(sqlStore, sqlStore.Cfg))
		publicdashboardStore := database.ProvideStore(sqlStore)
		templateVars := make([]map[string]interface{}, 1)
		dashboard := insertTestDashboard(t, dashboardStore, "testDashie", 1, 0, true, templateVars, nil)

		service := &PublicDashboardServiceImpl{
			log:   log.New("test.logger"),
			store: publicdashboardStore,
		}

		dto := &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       7,
			PublicDashboard: &PublicDashboard{
				IsEnabled:    true,
				DashboardUid: "NOTTHESAME",
				OrgId:        9999999,
			},
		}

		_, err := service.Save(context.Background(), SignedInUser, dto)
		require.Error(t, err)
	})

	t.Run("Pubdash access token generation throws an error and pubdash is not persisted", func(t *testing.T) {
		dashboard := models.NewDashboard("testDashie")
		pubdash := &PublicDashboard{
			IsEnabled:          true,
			AnnotationsEnabled: false,
			DashboardUid:       "NOTTHESAME",
			OrgId:              9999999,
			TimeSettings:       timeSettings,
		}

		publicDashboardStore := &FakePublicDashboardStore{}
		publicDashboardStore.On("FindDashboard", mock.Anything, mock.Anything, mock.Anything).Return(dashboard, nil)
		publicDashboardStore.On("Find", mock.Anything, mock.Anything).Return(nil, nil)
		publicDashboardStore.On("FindByAccessToken", mock.Anything, mock.Anything).Return(pubdash, nil)
		publicDashboardStore.On("NewPublicDashboardUid", mock.Anything).Return("an-uid", nil)

		service := &PublicDashboardServiceImpl{
			log:   log.New("test.logger"),
			store: publicDashboardStore,
		}

		dto := &SavePublicDashboardConfigDTO{
			DashboardUid: "an-id",
			OrgId:        8,
			UserId:       7,
			PublicDashboard: &PublicDashboard{
				IsEnabled:    true,
				DashboardUid: "NOTTHESAME",
				OrgId:        9999999,
			},
		}

		_, err := service.Save(context.Background(), SignedInUser, dto)

		require.Error(t, err)
		require.Equal(t, err, ErrPublicDashboardFailedGenerateAccessToken)
		publicDashboardStore.AssertNotCalled(t, "Save")
	})
}

func TestUpdatePublicDashboard(t *testing.T) {
	t.Run("Updating public dashboard", func(t *testing.T) {
		sqlStore := db.InitTestDB(t)
		dashboardStore := dashboardsDB.ProvideDashboardStore(sqlStore, sqlStore.Cfg, featuremgmt.WithFeatures(), tagimpl.ProvideService(sqlStore, sqlStore.Cfg))
		publicdashboardStore := database.ProvideStore(sqlStore)
		dashboard := insertTestDashboard(t, dashboardStore, "testDashie", 1, 0, true, []map[string]interface{}{}, nil)

		service := &PublicDashboardServiceImpl{
			log:   log.New("test.logger"),
			store: publicdashboardStore,
		}

		dto := &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       7,
			PublicDashboard: &PublicDashboard{
				AnnotationsEnabled: false,
				IsEnabled:          true,
				TimeSettings:       timeSettings,
			},
		}

		savedPubdash, err := service.Save(context.Background(), SignedInUser, dto)
		require.NoError(t, err)

		// attempt to overwrite settings
		dto = &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       8,
			PublicDashboard: &PublicDashboard{
				Uid:          savedPubdash.Uid,
				OrgId:        9,
				DashboardUid: "abc1234",
				CreatedBy:    9,
				CreatedAt:    time.Time{},

				IsEnabled:          true,
				AnnotationsEnabled: true,
				TimeSettings:       timeSettings,
				AccessToken:        "NOTAREALUUID",
			},
		}

		// Since the dto.PublicDashboard has a uid, this will call
		// service.updatePublicDashboard
		updatedPubdash, err := service.Save(context.Background(), SignedInUser, dto)
		require.NoError(t, err)

		// don't get updated
		assert.Equal(t, savedPubdash.DashboardUid, updatedPubdash.DashboardUid)
		assert.Equal(t, savedPubdash.OrgId, updatedPubdash.OrgId)
		assert.Equal(t, savedPubdash.CreatedAt, updatedPubdash.CreatedAt)
		assert.Equal(t, savedPubdash.CreatedBy, updatedPubdash.CreatedBy)
		assert.Equal(t, savedPubdash.AccessToken, updatedPubdash.AccessToken)

		// gets updated
		assert.Equal(t, dto.PublicDashboard.IsEnabled, updatedPubdash.IsEnabled)
		assert.Equal(t, dto.PublicDashboard.AnnotationsEnabled, updatedPubdash.AnnotationsEnabled)
		assert.Equal(t, dto.PublicDashboard.TimeSettings, updatedPubdash.TimeSettings)
		assert.Equal(t, dto.UserId, updatedPubdash.UpdatedBy)
		assert.NotEqual(t, &time.Time{}, updatedPubdash.UpdatedAt)
	})

	t.Run("Updating set empty time settings", func(t *testing.T) {
		sqlStore := db.InitTestDB(t)
		dashboardStore := dashboardsDB.ProvideDashboardStore(sqlStore, sqlStore.Cfg, featuremgmt.WithFeatures(), tagimpl.ProvideService(sqlStore, sqlStore.Cfg))
		publicdashboardStore := database.ProvideStore(sqlStore)
		dashboard := insertTestDashboard(t, dashboardStore, "testDashie", 1, 0, true, []map[string]interface{}{}, nil)

		service := &PublicDashboardServiceImpl{
			log:   log.New("test.logger"),
			store: publicdashboardStore,
		}

		dto := &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       7,
			PublicDashboard: &PublicDashboard{
				IsEnabled:    true,
				TimeSettings: timeSettings,
			},
		}

		// Since the dto.PublicDashboard has a uid, this will call
		// service.updatePublicDashboard
		savedPubdash, err := service.Save(context.Background(), SignedInUser, dto)
		require.NoError(t, err)

		// attempt to overwrite settings
		dto = &SavePublicDashboardConfigDTO{
			DashboardUid: dashboard.Uid,
			OrgId:        dashboard.OrgId,
			UserId:       8,
			PublicDashboard: &PublicDashboard{
				Uid:          savedPubdash.Uid,
				OrgId:        9,
				DashboardUid: "abc1234",
				CreatedBy:    9,
				CreatedAt:    time.Time{},

				IsEnabled:   true,
				AccessToken: "NOTAREALUUID",
			},
		}

		updatedPubdash, err := service.Save(context.Background(), SignedInUser, dto)
		require.NoError(t, err)

		assert.Equal(t, &TimeSettings{}, updatedPubdash.TimeSettings)
	})
}

func insertTestDashboard(t *testing.T, dashboardStore *dashboardsDB.DashboardStore, title string, orgId int64,
	folderId int64, isFolder bool, templateVars []map[string]interface{}, customPanels []interface{}, tags ...interface{}) *models.Dashboard {
	t.Helper()

	var dashboardPanels []interface{}
	if customPanels != nil {
		dashboardPanels = customPanels
	} else {
		dashboardPanels = []interface{}{
			map[string]interface{}{
				"id": 1,
				"datasource": map[string]interface{}{
					"uid": "ds1",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"datasource": map[string]interface{}{
							"type": "mysql",
							"uid":  "ds1",
						},
						"refId": "A",
					},
					map[string]interface{}{
						"datasource": map[string]interface{}{
							"type": "prometheus",
							"uid":  "ds2",
						},
						"refId": "B",
					},
				},
			},
			map[string]interface{}{
				"id": 2,
				"datasource": map[string]interface{}{
					"uid": "ds3",
				},
				"targets": []interface{}{
					map[string]interface{}{
						"datasource": map[string]interface{}{
							"type": "mysql",
							"uid":  "ds3",
						},
						"refId": "C",
					},
				},
			},
		}
	}

	cmd := models.SaveDashboardCommand{
		OrgId:    orgId,
		FolderId: folderId,
		IsFolder: isFolder,
		Dashboard: simplejson.NewFromAny(map[string]interface{}{
			"id":     nil,
			"title":  title,
			"tags":   tags,
			"panels": dashboardPanels,
			"templating": map[string]interface{}{
				"list": templateVars,
			},
			"time": map[string]interface{}{
				"from": "2022-09-01T00:00:00.000Z",
				"to":   "2022-09-01T12:00:00.000Z",
			},
		}),
	}
	dash, err := dashboardStore.SaveDashboard(context.Background(), cmd)
	require.NoError(t, err)
	require.NotNil(t, dash)
	dash.Data.Set("id", dash.Id)
	dash.Data.Set("uid", dash.Uid)
	return dash
}

func TestPublicDashboardServiceImpl_getSafeIntervalAndMaxDataPoints(t *testing.T) {
	type args struct {
		reqDTO PublicDashboardQueryDTO
		ts     TimeSettings
	}
	tests := []struct {
		name                  string
		args                  args
		wantSafeInterval      int64
		wantSafeMaxDataPoints int64
	}{
		{
			name: "return original interval",
			args: args{
				reqDTO: PublicDashboardQueryDTO{
					IntervalMs:    10000,
					MaxDataPoints: 300,
				},
				ts: TimeSettings{
					From: "now-3h",
					To:   "now",
				},
			},
			wantSafeInterval:      10000,
			wantSafeMaxDataPoints: 300,
		},
		{
			name: "return safe interval because of a small interval",
			args: args{
				reqDTO: PublicDashboardQueryDTO{
					IntervalMs:    1000,
					MaxDataPoints: 300,
				},
				ts: TimeSettings{
					From: "now-6h",
					To:   "now",
				},
			},
			wantSafeInterval:      2000,
			wantSafeMaxDataPoints: 11000,
		},
		{
			name: "return safe interval for long time range",
			args: args{
				reqDTO: PublicDashboardQueryDTO{
					IntervalMs:    100,
					MaxDataPoints: 300,
				},
				ts: TimeSettings{
					From: "now-90d",
					To:   "now",
				},
			},
			wantSafeInterval:      600000,
			wantSafeMaxDataPoints: 11000,
		},
		{
			name: "return safe interval when reqDTO is empty",
			args: args{
				reqDTO: PublicDashboardQueryDTO{},
				ts: TimeSettings{
					From: "now-90d",
					To:   "now",
				},
			},
			wantSafeInterval:      600000,
			wantSafeMaxDataPoints: 11000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := &PublicDashboardServiceImpl{
				intervalCalculator: intervalv2.NewCalculator(),
			}
			got, got1 := pd.getSafeIntervalAndMaxDataPoints(tt.args.reqDTO, tt.args.ts)
			assert.Equalf(t, tt.wantSafeInterval, got, "getSafeIntervalAndMaxDataPoints(%v, %v)", tt.args.reqDTO, tt.args.ts)
			assert.Equalf(t, tt.wantSafeMaxDataPoints, got1, "getSafeIntervalAndMaxDataPoints(%v, %v)", tt.args.reqDTO, tt.args.ts)
		})
	}
}

func TestDashboardEnabledChanged(t *testing.T) {
	t.Run("created isEnabled: false", func(t *testing.T) {
		assert.False(t, publicDashboardIsEnabledChanged(nil, &PublicDashboard{IsEnabled: false}))
	})

	t.Run("created isEnabled: true", func(t *testing.T) {
		assert.True(t, publicDashboardIsEnabledChanged(nil, &PublicDashboard{IsEnabled: true}))
	})

	t.Run("updated isEnabled same", func(t *testing.T) {
		assert.False(t, publicDashboardIsEnabledChanged(&PublicDashboard{IsEnabled: true}, &PublicDashboard{IsEnabled: true}))
	})

	t.Run("updated isEnabled changed", func(t *testing.T) {
		assert.True(t, publicDashboardIsEnabledChanged(&PublicDashboard{IsEnabled: false}, &PublicDashboard{IsEnabled: true}))
	})
}

func CreateDatasource(dsType string, uid string) struct {
	Type *string `json:"type,omitempty"`
	Uid  *string `json:"uid,omitempty"`
} {
	return struct {
		Type *string `json:"type,omitempty"`
		Uid  *string `json:"uid,omitempty"`
	}{
		Type: &dsType,
		Uid:  &uid,
	}
}

func AddAnnotationsToDashboard(t *testing.T, dash *models.Dashboard, annotations []DashAnnotation) *models.Dashboard {
	type annotationsDto struct {
		List []DashAnnotation `json:"list"`
	}
	annos := annotationsDto{}
	annos.List = annotations
	annoJSON, err := json.Marshal(annos)
	require.NoError(t, err)

	dashAnnos, err := simplejson.NewJson(annoJSON)
	require.NoError(t, err)

	dash.Data.Set("annotations", dashAnnos)

	return dash
}

func TestPublicDashboardServiceImpl_ListPublicDashboards(t *testing.T) {
	type args struct {
		ctx   context.Context
		u     *user.SignedInUser
		orgId int64
	}

	testCases := []struct {
		name         string
		args         args
		evaluateFunc func(c context.Context, u *user.SignedInUser, e accesscontrol.Evaluator) (bool, error)
		want         []PublicDashboardListResponse
		wantErr      assert.ErrorAssertionFunc
	}{
		{
			name: "should return empty list when user does not have permissions to read any dashboard",
			args: args{
				ctx:   context.Background(),
				u:     &user.SignedInUser{OrgID: 1},
				orgId: 1,
			},
			want:    []PublicDashboardListResponse{},
			wantErr: assert.NoError,
		},
		{
			name: "should return all dashboards when has permissions",
			args: args{
				ctx: context.Background(),
				u: &user.SignedInUser{OrgID: 1, Permissions: map[int64]map[string][]string{
					1: {"dashboards:read": {
						"dashboards:uid:0S6TmO67z", "dashboards:uid:1S6TmO67z", "dashboards:uid:2S6TmO67z", "dashboards:uid:9S6TmO67z",
					}}},
				},
				orgId: 1,
			},
			want: []PublicDashboardListResponse{
				{
					Uid:          "0GwW7mgVk",
					AccessToken:  "0b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "0S6TmO67z",
					Title:        "my zero dashboard",
					IsEnabled:    true,
				},
				{
					Uid:          "1GwW7mgVk",
					AccessToken:  "1b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "1S6TmO67z",
					Title:        "my first dashboard",
					IsEnabled:    true,
				},
				{
					Uid:          "2GwW7mgVk",
					AccessToken:  "2b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "2S6TmO67z",
					Title:        "my second dashboard",
					IsEnabled:    false,
				},
				{
					Uid:          "9GwW7mgVk",
					AccessToken:  "deletedashboardaccesstoken",
					DashboardUid: "9S6TmO67z",
					Title:        "",
					IsEnabled:    true,
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return only dashboards with permissions",
			args: args{
				ctx: context.Background(),
				u: &user.SignedInUser{OrgID: 1, Permissions: map[int64]map[string][]string{
					1: {"dashboards:read": {"dashboards:uid:0S6TmO67z"}}},
				},
				orgId: 1,
			},
			want: []PublicDashboardListResponse{
				{
					Uid:          "0GwW7mgVk",
					AccessToken:  "0b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "0S6TmO67z",
					Title:        "my zero dashboard",
					IsEnabled:    true,
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "should return orphaned public dashboards",
			args: args{
				ctx: context.Background(),
				u: &user.SignedInUser{OrgID: 1, Permissions: map[int64]map[string][]string{
					1: {"dashboards:read": {"dashboards:uid:0S6TmO67z"}}},
				},
				orgId: 1,
			},
			evaluateFunc: func(c context.Context, u *user.SignedInUser, e accesscontrol.Evaluator) (bool, error) {
				return false, dashboards.ErrDashboardNotFound
			},
			want: []PublicDashboardListResponse{
				{
					Uid:          "0GwW7mgVk",
					AccessToken:  "0b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "0S6TmO67z",
					Title:        "my zero dashboard",
					IsEnabled:    true,
				},
				{
					Uid:          "1GwW7mgVk",
					AccessToken:  "1b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "1S6TmO67z",
					Title:        "my first dashboard",
					IsEnabled:    true,
				},
				{
					Uid:          "2GwW7mgVk",
					AccessToken:  "2b458cb7fe7f42c68712078bcacee6e3",
					DashboardUid: "2S6TmO67z",
					Title:        "my second dashboard",
					IsEnabled:    false,
				},
				{
					Uid:          "9GwW7mgVk",
					AccessToken:  "deletedashboardaccesstoken",
					DashboardUid: "9S6TmO67z",
					Title:        "",
					IsEnabled:    true,
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "errors different than not data found should be returned",
			args: args{
				ctx: context.Background(),
				u: &user.SignedInUser{OrgID: 1, Permissions: map[int64]map[string][]string{
					1: {"dashboards:read": {"dashboards:uid:0S6TmO67z"}}},
				},
				orgId: 1,
			},
			evaluateFunc: func(c context.Context, u *user.SignedInUser, e accesscontrol.Evaluator) (bool, error) {
				return false, dashboards.ErrDashboardCorrupt
			},
			want:    nil,
			wantErr: assert.Error,
		},
	}

	mockedDashboards := []PublicDashboardListResponse{
		{
			Uid:          "0GwW7mgVk",
			AccessToken:  "0b458cb7fe7f42c68712078bcacee6e3",
			DashboardUid: "0S6TmO67z",
			Title:        "my zero dashboard",
			IsEnabled:    true,
		},
		{
			Uid:          "1GwW7mgVk",
			AccessToken:  "1b458cb7fe7f42c68712078bcacee6e3",
			DashboardUid: "1S6TmO67z",
			Title:        "my first dashboard",
			IsEnabled:    true,
		},
		{
			Uid:          "2GwW7mgVk",
			AccessToken:  "2b458cb7fe7f42c68712078bcacee6e3",
			DashboardUid: "2S6TmO67z",
			Title:        "my second dashboard",
			IsEnabled:    false,
		},
		{
			Uid:          "9GwW7mgVk",
			AccessToken:  "deletedashboardaccesstoken",
			DashboardUid: "9S6TmO67z",
			Title:        "",
			IsEnabled:    true,
		},
	}

	store := NewFakePublicDashboardStore(t)
	store.On("FindAll", mock.Anything, mock.Anything).
		Return(mockedDashboards, nil)

	ac := tests.SetupMockAccesscontrol(t,
		func(c context.Context, siu *user.SignedInUser, _ accesscontrol.Options) ([]accesscontrol.Permission, error) {
			return []accesscontrol.Permission{}, nil
		},
		false,
	)

	pd := &PublicDashboardServiceImpl{
		log:   log.New("test.logger"),
		store: store,
		ac:    ac,
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ac.EvaluateFunc = tt.evaluateFunc

			got, err := pd.FindAll(tt.args.ctx, tt.args.u, tt.args.orgId)
			if !tt.wantErr(t, err, fmt.Sprintf("FindAll(%v, %v, %v)", tt.args.ctx, tt.args.u, tt.args.orgId)) {
				return
			}
			assert.Equalf(t, tt.want, got, "FindAll(%v, %v, %v)", tt.args.ctx, tt.args.u, tt.args.orgId)
		})
	}
}

func TestPublicDashboardServiceImpl_NewPublicDashboardUid(t *testing.T) {
	mockedDashboard := &PublicDashboard{
		IsEnabled:          true,
		AnnotationsEnabled: false,
		DashboardUid:       "NOTTHESAME",
		OrgId:              9999999,
		TimeSettings:       timeSettings,
	}

	type args struct {
		ctx context.Context
	}

	type mockResponse struct {
		PublicDashboard *PublicDashboard
		Err             error
	}
	tests := []struct {
		name      string
		args      args
		mockStore *mockResponse
		want      string
		wantErr   assert.ErrorAssertionFunc
	}{
		{
			name:      "should return a new uid",
			args:      args{ctx: context.Background()},
			mockStore: &mockResponse{nil, nil},
			want:      "NOTTHESAME",
			wantErr:   assert.NoError,
		},
		{
			name:      "should return an error if the generated uid exists 3 times",
			args:      args{ctx: context.Background()},
			mockStore: &mockResponse{mockedDashboard, nil},
			want:      "",
			wantErr:   assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewFakePublicDashboardStore(t)
			store.On("Find", mock.Anything, mock.Anything).
				Return(tt.mockStore.PublicDashboard, tt.mockStore.Err)

			pd := &PublicDashboardServiceImpl{store: store}

			got, err := pd.NewPublicDashboardUid(tt.args.ctx)
			if !tt.wantErr(t, err, fmt.Sprintf("NewPublicDashboardUid(%v)", tt.args.ctx)) {
				return
			}

			if err == nil {
				assert.NotEqual(t, got, tt.want, "NewPublicDashboardUid(%v)", tt.args.ctx)
				assert.True(t, util.IsValidShortUID(got), "NewPublicDashboardUid(%v)", tt.args.ctx)
				store.AssertNumberOfCalls(t, "Find", 1)
			} else {
				store.AssertNumberOfCalls(t, "Find", 3)
				assert.True(t, errors.Is(err, ErrPublicDashboardFailedGenerateUniqueUid))
			}
		})
	}
}

func TestPublicDashboardServiceImpl_NewPublicDashboardAccessToken(t *testing.T) {
	mockedDashboard := &PublicDashboard{
		IsEnabled:          true,
		AnnotationsEnabled: false,
		DashboardUid:       "NOTTHESAME",
		OrgId:              9999999,
		TimeSettings:       timeSettings,
	}

	type args struct {
		ctx context.Context
	}

	type mockResponse struct {
		PublicDashboard *PublicDashboard
		Err             error
	}
	tests := []struct {
		name      string
		args      args
		mockStore *mockResponse
		want      string
		wantErr   assert.ErrorAssertionFunc
	}{
		{
			name:      "should return a new access token",
			args:      args{ctx: context.Background()},
			mockStore: &mockResponse{nil, nil},
			want:      "6522e152530f4ee76522e152530f4ee7",
			wantErr:   assert.NoError,
		},
		{
			name:      "should return an error if the generated access token exists 3 times",
			args:      args{ctx: context.Background()},
			mockStore: &mockResponse{mockedDashboard, nil},
			want:      "",
			wantErr:   assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewFakePublicDashboardStore(t)
			store.On("FindByAccessToken", mock.Anything, mock.Anything).
				Return(tt.mockStore.PublicDashboard, tt.mockStore.Err)

			pd := &PublicDashboardServiceImpl{store: store}

			got, err := pd.NewPublicDashboardAccessToken(tt.args.ctx)
			if !tt.wantErr(t, err, fmt.Sprintf("NewPublicDashboardAccessToken(%v)", tt.args.ctx)) {
				return
			}

			if err == nil {
				assert.NotEqual(t, got, tt.want, "NewPublicDashboardAccessToken(%v)", tt.args.ctx)
				assert.True(t, tokens.IsValidAccessToken(got), "NewPublicDashboardAccessToken(%v)", tt.args.ctx)
				store.AssertNumberOfCalls(t, "FindByAccessToken", 1)
			} else {
				store.AssertNumberOfCalls(t, "FindByAccessToken", 3)
				assert.True(t, errors.Is(err, ErrPublicDashboardFailedGenerateAccessToken))
			}
		})
	}
}
