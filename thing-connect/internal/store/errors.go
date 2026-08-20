package store

import "errors"

// ErrSlotConflict is returned by CommitClaim when a concurrent request claimed the device first.
var ErrSlotConflict = errors.New("设备已被并发认领，请重试")

// ErrDeviceNotFound is returned when a transactional device mutation cannot
// find the requested device row.
var ErrDeviceNotFound = errors.New("设备不存在")

// ErrPoolEmpty is returned by CommitBindFromPool when the device pool is exhausted.
var ErrPoolEmpty = errors.New("设备池已耗尽")

// ErrQuotaEmpty is returned by Commit* methods when the user has no bind quota remaining.
var ErrQuotaEmpty = errors.New("配额不足")

// ErrMACAlreadyBound is returned by CommitBindFromPool when (mac, user_id) is
// already bound. A concurrent or repeated bind of the same MAC by the same user
// returns the existing device_id (carried in the returned deviceID) instead of
// allocating a new one — so one user never ends up with two device_ids for one
// MAC. The DB-level UNIQUE(mac_user_key) is the last-line guard; this error is
// the in-transaction check that gives a clean result without relying on a 1062.
var ErrMACAlreadyBound = errors.New("该MAC已绑定到本用户，返回已有设备")
