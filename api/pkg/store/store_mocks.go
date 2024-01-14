// Code generated by MockGen. DO NOT EDIT.
// Source: types.go

// Package store is a generated GoMock package.
package store

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	types "github.com/lukemarsden/helix/api/pkg/types"
)

// MockStore is a mock of Store interface.
type MockStore struct {
	ctrl     *gomock.Controller
	recorder *MockStoreMockRecorder
}

// MockStoreMockRecorder is the mock recorder for MockStore.
type MockStoreMockRecorder struct {
	mock *MockStore
}

// NewMockStore creates a new mock instance.
func NewMockStore(ctrl *gomock.Controller) *MockStore {
	mock := &MockStore{ctrl: ctrl}
	mock.recorder = &MockStoreMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockStore) EXPECT() *MockStoreMockRecorder {
	return m.recorder
}

// CheckAPIKey mocks base method.
func (m *MockStore) CheckAPIKey(ctx context.Context, apiKey string) (*types.ApiKey, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CheckAPIKey", ctx, apiKey)
	ret0, _ := ret[0].(*types.ApiKey)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CheckAPIKey indicates an expected call of CheckAPIKey.
func (mr *MockStoreMockRecorder) CheckAPIKey(ctx, apiKey interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CheckAPIKey", reflect.TypeOf((*MockStore)(nil).CheckAPIKey), ctx, apiKey)
}

// CreateAPIKey mocks base method.
func (m *MockStore) CreateAPIKey(ctx context.Context, owner OwnerQuery, name string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateAPIKey", ctx, owner, name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateAPIKey indicates an expected call of CreateAPIKey.
func (mr *MockStoreMockRecorder) CreateAPIKey(ctx, owner, name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateAPIKey", reflect.TypeOf((*MockStore)(nil).CreateAPIKey), ctx, owner, name)
}

// CreateBot mocks base method.
func (m *MockStore) CreateBot(ctx context.Context, Bot types.Bot) (*types.Bot, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateBot", ctx, Bot)
	ret0, _ := ret[0].(*types.Bot)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateBot indicates an expected call of CreateBot.
func (mr *MockStoreMockRecorder) CreateBot(ctx, Bot interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateBot", reflect.TypeOf((*MockStore)(nil).CreateBot), ctx, Bot)
}

// CreateSession mocks base method.
func (m *MockStore) CreateSession(ctx context.Context, session types.Session) (*types.Session, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateSession", ctx, session)
	ret0, _ := ret[0].(*types.Session)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateSession indicates an expected call of CreateSession.
func (mr *MockStoreMockRecorder) CreateSession(ctx, session interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateSession", reflect.TypeOf((*MockStore)(nil).CreateSession), ctx, session)
}

// CreateUserMeta mocks base method.
func (m *MockStore) CreateUserMeta(ctx context.Context, UserMeta types.UserMeta) (*types.UserMeta, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateUserMeta", ctx, UserMeta)
	ret0, _ := ret[0].(*types.UserMeta)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateUserMeta indicates an expected call of CreateUserMeta.
func (mr *MockStoreMockRecorder) CreateUserMeta(ctx, UserMeta interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateUserMeta", reflect.TypeOf((*MockStore)(nil).CreateUserMeta), ctx, UserMeta)
}

// DeleteAPIKey mocks base method.
func (m *MockStore) DeleteAPIKey(ctx context.Context, apiKey types.ApiKey) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteAPIKey", ctx, apiKey)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteAPIKey indicates an expected call of DeleteAPIKey.
func (mr *MockStoreMockRecorder) DeleteAPIKey(ctx, apiKey interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteAPIKey", reflect.TypeOf((*MockStore)(nil).DeleteAPIKey), ctx, apiKey)
}

// DeleteBot mocks base method.
func (m *MockStore) DeleteBot(ctx context.Context, id string) (*types.Bot, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteBot", ctx, id)
	ret0, _ := ret[0].(*types.Bot)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DeleteBot indicates an expected call of DeleteBot.
func (mr *MockStoreMockRecorder) DeleteBot(ctx, id interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteBot", reflect.TypeOf((*MockStore)(nil).DeleteBot), ctx, id)
}

// DeleteSession mocks base method.
func (m *MockStore) DeleteSession(ctx context.Context, id string) (*types.Session, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteSession", ctx, id)
	ret0, _ := ret[0].(*types.Session)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DeleteSession indicates an expected call of DeleteSession.
func (mr *MockStoreMockRecorder) DeleteSession(ctx, id interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteSession", reflect.TypeOf((*MockStore)(nil).DeleteSession), ctx, id)
}

// EnsureUserMeta mocks base method.
func (m *MockStore) EnsureUserMeta(ctx context.Context, UserMeta types.UserMeta) (*types.UserMeta, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EnsureUserMeta", ctx, UserMeta)
	ret0, _ := ret[0].(*types.UserMeta)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// EnsureUserMeta indicates an expected call of EnsureUserMeta.
func (mr *MockStoreMockRecorder) EnsureUserMeta(ctx, UserMeta interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EnsureUserMeta", reflect.TypeOf((*MockStore)(nil).EnsureUserMeta), ctx, UserMeta)
}

// GetAPIKeys mocks base method.
func (m *MockStore) GetAPIKeys(ctx context.Context, query OwnerQuery) ([]*types.ApiKey, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetAPIKeys", ctx, query)
	ret0, _ := ret[0].([]*types.ApiKey)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetAPIKeys indicates an expected call of GetAPIKeys.
func (mr *MockStoreMockRecorder) GetAPIKeys(ctx, query interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetAPIKeys", reflect.TypeOf((*MockStore)(nil).GetAPIKeys), ctx, query)
}

// GetBot mocks base method.
func (m *MockStore) GetBot(ctx context.Context, id string) (*types.Bot, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBot", ctx, id)
	ret0, _ := ret[0].(*types.Bot)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBot indicates an expected call of GetBot.
func (mr *MockStoreMockRecorder) GetBot(ctx, id interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBot", reflect.TypeOf((*MockStore)(nil).GetBot), ctx, id)
}

// GetBots mocks base method.
func (m *MockStore) GetBots(ctx context.Context, query GetBotsQuery) ([]*types.Bot, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBots", ctx, query)
	ret0, _ := ret[0].([]*types.Bot)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBots indicates an expected call of GetBots.
func (mr *MockStoreMockRecorder) GetBots(ctx, query interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBots", reflect.TypeOf((*MockStore)(nil).GetBots), ctx, query)
}

// GetSession mocks base method.
func (m *MockStore) GetSession(ctx context.Context, id string) (*types.Session, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetSession", ctx, id)
	ret0, _ := ret[0].(*types.Session)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetSession indicates an expected call of GetSession.
func (mr *MockStoreMockRecorder) GetSession(ctx, id interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetSession", reflect.TypeOf((*MockStore)(nil).GetSession), ctx, id)
}

// GetSessions mocks base method.
func (m *MockStore) GetSessions(ctx context.Context, query GetSessionsQuery) ([]*types.Session, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetSessions", ctx, query)
	ret0, _ := ret[0].([]*types.Session)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetSessions indicates an expected call of GetSessions.
func (mr *MockStoreMockRecorder) GetSessions(ctx, query interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetSessions", reflect.TypeOf((*MockStore)(nil).GetSessions), ctx, query)
}

// GetSessionsCounter mocks base method.
func (m *MockStore) GetSessionsCounter(ctx context.Context, query GetSessionsQuery) (*types.Counter, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetSessionsCounter", ctx, query)
	ret0, _ := ret[0].(*types.Counter)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetSessionsCounter indicates an expected call of GetSessionsCounter.
func (mr *MockStoreMockRecorder) GetSessionsCounter(ctx, query interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetSessionsCounter", reflect.TypeOf((*MockStore)(nil).GetSessionsCounter), ctx, query)
}

// GetUserMeta mocks base method.
func (m *MockStore) GetUserMeta(ctx context.Context, id string) (*types.UserMeta, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetUserMeta", ctx, id)
	ret0, _ := ret[0].(*types.UserMeta)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetUserMeta indicates an expected call of GetUserMeta.
func (mr *MockStoreMockRecorder) GetUserMeta(ctx, id interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetUserMeta", reflect.TypeOf((*MockStore)(nil).GetUserMeta), ctx, id)
}

// UpdateBot mocks base method.
func (m *MockStore) UpdateBot(ctx context.Context, Bot types.Bot) (*types.Bot, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateBot", ctx, Bot)
	ret0, _ := ret[0].(*types.Bot)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateBot indicates an expected call of UpdateBot.
func (mr *MockStoreMockRecorder) UpdateBot(ctx, Bot interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateBot", reflect.TypeOf((*MockStore)(nil).UpdateBot), ctx, Bot)
}

// UpdateSession mocks base method.
func (m *MockStore) UpdateSession(ctx context.Context, session types.Session) (*types.Session, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateSession", ctx, session)
	ret0, _ := ret[0].(*types.Session)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateSession indicates an expected call of UpdateSession.
func (mr *MockStoreMockRecorder) UpdateSession(ctx, session interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateSession", reflect.TypeOf((*MockStore)(nil).UpdateSession), ctx, session)
}

// UpdateSessionMeta mocks base method.
func (m *MockStore) UpdateSessionMeta(ctx context.Context, data types.SessionMetaUpdate) (*types.Session, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateSessionMeta", ctx, data)
	ret0, _ := ret[0].(*types.Session)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateSessionMeta indicates an expected call of UpdateSessionMeta.
func (mr *MockStoreMockRecorder) UpdateSessionMeta(ctx, data interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateSessionMeta", reflect.TypeOf((*MockStore)(nil).UpdateSessionMeta), ctx, data)
}

// UpdateUserMeta mocks base method.
func (m *MockStore) UpdateUserMeta(ctx context.Context, UserMeta types.UserMeta) (*types.UserMeta, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateUserMeta", ctx, UserMeta)
	ret0, _ := ret[0].(*types.UserMeta)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateUserMeta indicates an expected call of UpdateUserMeta.
func (mr *MockStoreMockRecorder) UpdateUserMeta(ctx, UserMeta interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateUserMeta", reflect.TypeOf((*MockStore)(nil).UpdateUserMeta), ctx, UserMeta)
}
