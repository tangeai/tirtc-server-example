package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/call"
)

type CallRooms struct{ client *redis.Client }

func NewCallRooms(client *redis.Client) *CallRooms {
	if client == nil {
		panic("call rooms: Redis client is required")
	}
	return &CallRooms{client: client}
}

var _ call.Rooms = (*CallRooms)(nil)

// These scripts target the project's standalone Redis deployment. Existing
// room/lock keys are preserved; they are not Redis Cluster hash-tagged keys.
const callRoomPrelude = callNotificationIndex + `
local function room(id)
 local key='room:'..id
 if redis.call('EXISTS',key)==0 then return nil end
 local fields=redis.call('HGETALL',key)
 local r={room_id=id}
 for i=1,#fields,2 do r[fields[i]]=fields[i+1] end
 r.targets=cjson.decode(r.targets or '[]')
 local rejected=redis.call('SMEMBERS',key..':rejected_by')
 if #rejected>0 then r.rejected_targets=rejected end
 return r
end
local function unlock(device,id)
 if device and device~='' and redis.call('GET','room:lock:'..device)==id then
  redis.call('DEL','room:lock:'..device)
 end
end
local function now_ms()
 local t=redis.call('TIME'); return tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
end
local function enqueue(r,target,kind,from,payload)
 local at=now_ms()
 local id=r.room_id..':'..redis.call('INCR','call:outbox:sequence')
 local e={id=id,room_id=r.room_id,target=target,kind=kind,from=from or '',payload=payload,created_at=at}
 local length=redis.call('RPUSH','call:outbox:room:'..r.room_id,cjson.encode(e))
 if length==1 and redis.call('ZSCORE','call:outbox:due',r.room_id) then discard_orphan(r.room_id) end
 redis.call('ZADD','call:outbox:due','NX',at,r.room_id)
 redis.call('ZADD','call:outbox:age','NX',at,r.room_id)
 redis.call('INCR','call:outbox:count')
end
local function release(r)
 unlock(r.caller,r.room_id)
 unlock(r.answered_by,r.room_id)
 local winner=redis.call('GET','room:'..r.room_id..':answered')
 unlock(winner,r.room_id)
 redis.call('DEL','room:'..r.room_id,'room:'..r.room_id..':answered','room:'..r.room_id..':rejected_by')
end
local function owned_room(device)
 return redis.call('GET','room:lock:'..device)
end
local function room_winner(id)
 return redis.call('GET','room:'..id..':answered')
end
local function room_ttl(id)
 return redis.call('PTTL','room:'..id)
end
local function insert_room(r,offline,ttl)
 local key='room:'..r.room_id
 redis.call('HSET',key,'caller',r.caller,'targets',cjson.encode(r.targets),'status','active','answered_by','','call_type',r.call_type,'created_at',r.created_at)
 redis.call('PEXPIRE',key,ttl)
 redis.call('SET','room:lock:'..r.caller,r.room_id,'PX',ttl)
 for _,target in ipairs(offline) do redis.call('SADD',key..':rejected_by',target) end
 redis.call('PEXPIRE',key..':rejected_by',ttl)
end
local function save_answer(id,actor,ttl)
 redis.call('SET','room:lock:'..actor,id,'PX',ttl)
 redis.call('SET','room:'..id..':answered',actor,'PX',ttl)
 redis.call('HSET','room:'..id,'status','answered','answered_by',actor)
end
local function add_rejection(id,actor)
 local key='room:'..id..':rejected_by'
 local added=redis.call('SADD',key,actor)==1
 redis.call('PEXPIRE',key,room_ttl(id))
 return added
end
local function rejected_by(id,actor)
 return redis.call('SISMEMBER','room:'..id..':rejected_by',actor)==1
end
`

var createCallRoom = redis.NewScript(callRoomPrelude + call.RoomTransitions + `
return create_room(cjson.decode(ARGV[1]),cjson.decode(ARGV[2]),tonumber(ARGV[3]))
`)
var getCallRoom = redis.NewScript(callRoomPrelude + `
local r=room(ARGV[1])
if not r then return '' end
return cjson.encode(r)
`)
var currentCallRoom = redis.NewScript(callRoomPrelude + `
local id=redis.call('GET','room:lock:'..ARGV[1])
if not id then return '' end
local r=room(id)
if not r then unlock(ARGV[1],id); return '' end
return cjson.encode(r)
`)
var answerCallRoom = redis.NewScript(callRoomPrelude + call.RoomTransitions + `
return answer_room(ARGV[1],ARGV[2],ARGV[3])
`)
var endCallRoom = redis.NewScript(callRoomPrelude + call.RoomTransitions + `
return end_room(ARGV[1],ARGV[2],ARGV[3]=='1',ARGV[4])
`)
var rejectCallRoom = redis.NewScript(callRoomPrelude + call.RoomTransitions + `
return reject_room(ARGV[1],ARGV[2],ARGV[3])
`)

func (s *CallRooms) Create(ctx context.Context, r call.Room, offline []string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("call rooms: invalid TTL")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	if offline == nil {
		offline = []string{}
	}
	rejected, err := json.Marshal(offline)
	if err != nil {
		return "", err
	}
	return createCallRoom.Run(ctx, s.client, []string{"room:" + r.RoomID}, body, rejected, ttl.Milliseconds()).Text()
}
func (s *CallRooms) read(ctx context.Context, script *redis.Script, id string) (*call.Room, error) {
	raw, err := script.Run(ctx, s.client, nil, id).Text()
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil //nolint:nilnil // Missing room is an optional result.
	}
	var r call.Room
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func (s *CallRooms) Get(ctx context.Context, id string) (*call.Room, error) {
	return s.read(ctx, getCallRoom, id)
}
func (s *CallRooms) Current(ctx context.Context, id string) (*call.Room, error) {
	return s.read(ctx, currentCallRoom, id)
}
func (s *CallRooms) transition(ctx context.Context, script *redis.Script, args []interface{}, out interface{}) error {
	raw, err := script.Run(ctx, s.client, nil, args...).Text()
	if err != nil {
		return err
	}
	switch raw {
	case "not_found":
		return call.ErrNotFound
	case "forbidden":
		return call.ErrForbidden
	case "answered":
		return call.ErrAnswered
	}
	return json.Unmarshal([]byte(raw), out)
}
func (s *CallRooms) Answer(ctx context.Context, id, caller, self string) (call.AnswerResult, error) {
	var out call.AnswerResult
	err := s.transition(ctx, answerCallRoom, []interface{}{id, caller, self}, &out)
	return out, err
}
func (s *CallRooms) End(ctx context.Context, id, self string, callerOnly bool, reason string) (*call.Room, error) {
	var r call.Room
	only := "0"
	if callerOnly {
		only = "1"
	}
	if err := s.transition(ctx, endCallRoom, []interface{}{id, self, only, reason}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func (s *CallRooms) Reject(ctx context.Context, id, self, reason string) (call.RejectResult, error) {
	var out call.RejectResult
	err := s.transition(ctx, rejectCallRoom, []interface{}{id, self, reason}, &out)
	return out, err
}
