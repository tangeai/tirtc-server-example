export interface Assignment { room_id: string; assignment_version: number; desired_state: string; state: string; room_code?: string; online?: boolean; online_count?: number; password_set?: boolean }
export const roomStates: Record<string, string> = { assigned: '已安排加入', waiting_device: '设备离线，等待上线', suspended: '设备忙，空闲后加入', connecting: '正在加入', joined: '已加入', connect_failed: '连接失败，设备将重试', left: '已退出', room_closed: '房间已解散' };
export function roomPayload(kind: 'create' | 'join', roomCode: string, password: string) {
  if (password && !/^[0-9]{4}$/.test(password)) throw new Error('密码应为 4 位数字');
  if (kind === 'join' && !/^[0-9]{6}$/.test(roomCode)) throw new Error('请输入 6 位房间号');
  return kind === 'join' ? { room_code: roomCode, password } : { password };
}
export function leavePayload(a: Assignment) { return { room_id: a.room_id, assignment_version: a.assignment_version }; }
