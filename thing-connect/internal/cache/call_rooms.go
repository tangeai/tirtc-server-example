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
const callRoomPrelude = `
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
local function contains(values,id)
 for _,v in ipairs(values) do if v==id then return true end end
 return false
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
 redis.call('RPUSH','call:outbox:room:'..r.room_id,cjson.encode(e))
 redis.call('ZADD','call:outbox:due','NX',at,r.room_id)
 redis.call('ZADD','call:outbox:age','NX',at,r.room_id)
 redis.call('INCR','call:outbox:count')
end
local function notify_end(r,self,reason,allTargets)
 local targets=r.targets
 if not allTargets then
  if r.caller==self and r.answered_by and r.answered_by~='' then targets={r.answered_by}
  elseif r.answered_by==self then targets={r.caller} end
 end
 local sent={}
 for _,target in ipairs(targets) do
  if target~=self and not sent[target] then
   enqueue(r,target,'room_cancel','',{room_id=r.room_id,reason=reason});sent[target]=true
  end
 end
end
local function release(r)
 unlock(r.caller,r.room_id)
 unlock(r.answered_by,r.room_id)
 local winner=redis.call('GET','room:'..r.room_id..':answered')
 unlock(winner,r.room_id)
 redis.call('DEL','room:'..r.room_id,'room:'..r.room_id..':answered','room:'..r.room_id..':rejected_by')
end
`

var createCallRoom = redis.NewScript(callRoomPrelude + `
local r=cjson.decode(ARGV[1])
local ttl=tonumber(ARGV[3])
local lock='room:lock:'..r.caller
local existing=redis.call('GET',lock)
if existing then return existing end
local key='room:'..r.room_id
redis.call('HSET',key,'caller',r.caller,'targets',cjson.encode(r.targets),'status','active','answered_by','','call_type',r.call_type,'created_at',r.created_at)
redis.call('PEXPIRE',key,ttl)
redis.call('SET',lock,r.room_id,'PX',ttl)
for _,id in ipairs(cjson.decode(ARGV[2])) do redis.call('SADD',key..':rejected_by',id) end
redis.call('PEXPIRE',key..':rejected_by',ttl)
local offline=cjson.decode(ARGV[2]); local sent={}
for _,target in ipairs(r.targets) do
 if not sent[target] and not contains(offline,target) then
  local name=r.caller
  if r.caller_names and r.caller_names[target] and r.caller_names[target]~='' then name=r.caller_names[target] end
  enqueue(r,target,'call_incoming',r.caller,{room_id=r.room_id,caller_id=r.caller,caller_name=name,call_type=r.call_type})
  sent[target]=true
 end
end
return ''
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
var answerCallRoom = redis.NewScript(callRoomPrelude + `
local r=room(ARGV[1])
if not r then return 'not_found' end
local self=ARGV[3]
if r.caller~=ARGV[2] or not contains(r.targets,self) then return 'forbidden' end
local key='room:'..r.room_id
local winner=redis.call('GET',key..':answered') or r.answered_by
if winner and winner~='' and winner~=self then return 'answered' end
if r.status=='answered' and r.answered_by==self then return cjson.encode({room=r,first=false}) end
local ttl=redis.call('PTTL',key)
if ttl<=0 then return 'not_found' end
local oldID=redis.call('GET','room:lock:'..self)
local old=nil
if oldID and oldID~=r.room_id then
 old=room(oldID)
 if old and (old.caller==self or old.answered_by==self) then release(old); notify_end(old,self,'caller_left',false)
 else old=nil; unlock(self,oldID) end
end
redis.call('SET','room:lock:'..self,r.room_id,'PX',ttl)
redis.call('SET',key..':answered',self,'PX',ttl)
redis.call('HSET',key,'status','answered','answered_by',self)
r.status='answered'; r.answered_by=self
 enqueue(r,r.caller,'callee_answered','',{room_id=r.room_id,callee_id=self})
 local sent={}
 for _,target in ipairs(r.targets) do
  if target~=self and not sent[target] then enqueue(r,target,'room_cancel','',{room_id=r.room_id,reason='accepted_by_other'});sent[target]=true end
 end
return cjson.encode({room=r,previous=old,first=true})
`)
var endCallRoom = redis.NewScript(callRoomPrelude + `
local r=room(ARGV[1])
if not r then return 'not_found' end
if r.caller~=ARGV[2] and (ARGV[3]=='1' or r.answered_by~=ARGV[2]) then return 'forbidden' end
release(r)
notify_end(r,ARGV[2],ARGV[4],ARGV[3]=='1' or (ARGV[4]=='unbound' and r.caller==ARGV[2]))
return cjson.encode(r)
`)
var rejectCallRoom = redis.NewScript(callRoomPrelude + `
local r=room(ARGV[1])
if not r then return 'not_found' end
if not contains(r.targets,ARGV[2]) then return 'forbidden' end
if r.status=='answered' or redis.call('EXISTS','room:'..r.room_id..':answered')==1 then
 return cjson.encode({room=r,applied=false,all_rejected=false})
end
local key='room:'..r.room_id..':rejected_by'
local applied=redis.call('SADD',key,ARGV[2])==1
redis.call('PEXPIRE',key,redis.call('PTTL','room:'..r.room_id))
local all=true
for _,id in ipairs(r.targets) do if redis.call('SISMEMBER',key,id)==0 then all=false; break end end
if applied then enqueue(r,r.caller,'call_reject',ARGV[2],{room_id=r.room_id,reason=ARGV[3]}) end
if all then
 release(r)
 enqueue(r,r.caller,'room_cancel','',{room_id=r.room_id,reason='all_rejected'})
end
return cjson.encode({room=r,applied=applied,all_rejected=all})
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
