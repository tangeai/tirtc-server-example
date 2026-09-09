const $ = (id) => document.getElementById(id);
const token = localStorage.getItem("token");
if (!token) location.href = "/login";

let resetDeviceID = null;
let editingDeviceID = null;
let devices = [];

function escapeHTML(value) {
  return String(value ?? "").replace(
    /[&<>'"]/g,
    (char) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        "'": "&#39;",
        '"': "&quot;",
      })[char],
  );
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    signal: AbortSignal.timeout(10000),
    ...opts,
    headers: {
      Authorization: "Bearer " + token,
      "Content-Type": "application/json",
      ...(opts.headers || {}),
    },
  });
  const body = await res.json();
  if (res.status === 401 || body.code === 401) {
    location.href = "/login";
    throw Error("登录已过期");
  }
  return body;
}

function toast(msg, error = false) {
  const el = $("toast");
  el.textContent = msg;
  el.style.borderColor = error ? "rgba(239,68,68,0.4)" : "rgba(16,185,129,0.4)";
  el.classList.add("show");
  setTimeout(() => el.classList.remove("show"), 2600);
}

async function goAdd() {
  const quotaData = await api("/v1/user/quota");
  if (quotaData.code === 200 && quotaData.data.quota <= 0) {
    toast("设备额度已用尽，请联系管理员申请扩容", true);
    return;
  }
  location.href = "/bind";
}

async function loadDevices() {
  const generation = ++summaryGeneration;
  let devData;
  try {
    devData = await api("/v1/user/device/list");
  } catch {
    toast("设备列表加载失败，请刷新", true);
    return;
  }
  if (generation !== summaryGeneration) return;
  if (devData.code !== 200) {
    toast(devData.msg || "设备列表加载失败，请刷新", true);
    return;
  }

  const grid = $("device-grid");

  if (devData.code !== 200 || !devData.data?.length) {
    devices = [];
    $("device-count").textContent = "";
    grid.innerHTML = `
      <div class="col-span-full flex flex-col items-center py-20 text-center">
        <div class="w-16 h-16 mb-5 rounded-2xl bg-emerald-50 border border-dashed border-emerald-200 flex items-center justify-center text-emerald-500">
          <svg class="w-7 h-7" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
            <rect x="2" y="3" width="20" height="14" rx="2"/>
            <path d="M8 21h8M12 17v4"/>
          </svg>
        </div>
        <p class="text-base font-semibold text-gray-800 mb-2">还没有设备</p>
        <p class="text-sm text-gray-400">点击"添加设备"，输入验证码完成绑定</p>
      </div>`;
    return;
  }

  devices = devData.data;
  $("device-count").textContent = `(${devData.data.length})`;

  renderCards();
  await refreshSummaries();
}

function renderCards() {
  closeDeviceMenu();
  $("device-grid").innerHTML = devices
    .map((d) => {
      const id = escapeHTML(d.device_id),
        name = escapeHTML(d.device_name || "未命名设备");
      return `<article class="bg-white border border-gray-200 rounded-2xl overflow-hidden shadow-sm" data-card-device="${id}">
      <div class="px-4 pt-4 pb-3 min-w-0">
        <div class="flex items-center gap-3 min-w-0">
          <span data-device-icon class="w-10 h-10 shrink-0 flex items-center justify-center rounded-xl border ${d.online ? "bg-emerald-50 border-emerald-100 text-emerald-600" : "bg-gray-50 border-gray-200 text-gray-400"}" aria-hidden="true">
            <svg class="w-6 h-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" focusable="false">
              <rect x="3" y="4" width="18" height="13" rx="2.5"/>
              <path d="M9 21h6M12 17v4"/>
            </svg>
          </span>
          <div class="min-w-0 flex-1"><h2 class="text-base font-semibold text-gray-900 truncate" title="${name}">${name}</h2>
</div>
          <button type="button" data-device-id="${id}" onclick="openDeviceMenu(this.dataset.deviceId, this)" class="shrink-0 px-2.5 py-2 text-xs text-gray-500 border border-gray-200 rounded-lg hover:bg-gray-50 transition-colors" aria-haspopup="menu" aria-controls="device-menu" aria-expanded="false" aria-label="${name}更多操作">更多 ▾</button>
        </div><div class="flex items-center justify-between gap-3 mt-3 min-w-0">
          <p class="font-mono text-xs text-gray-400 truncate min-w-0" title="${id}">ID：${id}</p>
          <span data-online-status class="inline-flex items-center gap-1.5 shrink-0 text-xs ${d.online ? "text-emerald-600" : "text-gray-400"}"><span class="w-1.5 h-1.5 shrink-0 rounded-full ${d.online ? "bg-emerald-500" : "bg-gray-400"}" aria-hidden="true"></span>${d.online ? "在线" : "离线"}</span>
        </div>
      </div>
      <div class="device-summary"><button type="button" data-device-id="${id}" onclick="goConfig(this.dataset.deviceId)"><b>AI 角色 <span aria-hidden="true">›</span></b><p data-role>正在查询…</p></button><button type="button" data-device-id="${id}" onclick="goRoom(this.dataset.deviceId)"><b>多人对讲 <span aria-hidden="true">›</span></b><p data-room>正在查询…</p></button></div>
      <div class="feature-buttons grid grid-cols-4 gap-1 px-3 py-2 border-t border-gray-100">
        <button type="button" class="flex flex-col items-center justify-center gap-1.5 min-h-[52px] rounded-lg text-xs text-gray-600 hover:bg-emerald-50 hover:text-emerald-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 transition-colors" data-device-id="${id}" onclick="goLive(this.dataset.deviceId)"><svg class="w-4 h-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M9 21h6M12 17v4"/></svg><span>实时</span></button>
        <button type="button" class="flex flex-col items-center justify-center gap-1.5 min-h-[52px] rounded-lg text-xs text-gray-600 hover:bg-emerald-50 hover:text-emerald-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 transition-colors" data-device-id="${id}" onclick="goContacts(this.dataset.deviceId)"><svg class="w-4 h-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><circle cx="9" cy="8" r="3"/><path d="M3 20v-2a6 6 0 0 1 12 0v2M16 5a3 3 0 0 1 0 6M21 20v-2a6 6 0 0 0-3-5"/></svg><span>联系人</span></button>
        <button type="button" class="flex flex-col items-center justify-center gap-1.5 min-h-[52px] rounded-lg text-xs text-gray-600 hover:bg-emerald-50 hover:text-emerald-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 transition-colors" data-device-id="${id}" onclick="goConfig(this.dataset.deviceId)"><svg class="w-4 h-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><rect x="4" y="7" width="16" height="13" rx="3"/><path d="M12 3v4M1 12v4M23 12v4M9 16h6"/><path d="M9 11v1M15 11v1"/></svg><span>AI 角色</span></button>
        <button type="button" class="flex flex-col items-center justify-center gap-1.5 min-h-[52px] rounded-lg text-xs text-gray-600 hover:bg-emerald-50 hover:text-emerald-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 transition-colors" data-device-id="${id}" onclick="goRoom(this.dataset.deviceId)"><svg class="w-4 h-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M21 11a8 8 0 0 1-8 8H7l-4 3v-6a8 8 0 1 1 18-5Z"/><path d="M8 10v3M12 8v7M16 10v3"/></svg><span>多人对讲<span data-room-dot class="room-dot" hidden aria-label="有对讲关系"></span></span></button>
      </div></article>`;
    })
    .join("");
}

let summaryGeneration = 0,
  summaryBusy = false;
function cardFor(id) {
  return Array.from(document.querySelectorAll("[data-card-device]")).find(
    (c) => c.dataset.cardDevice === id,
  );
}
function updateOnlineStatus(card, online) {
  const status = card.querySelector("[data-online-status]");
  const dot = status.firstElementChild;
  const icon = card.querySelector("[data-device-icon]");
  for (const [element, yes, no] of [
    [status, "text-emerald-600", "text-gray-400"],
    [dot, "bg-emerald-500", "bg-gray-400"],
    [icon, "bg-emerald-50", "bg-gray-50"],
    [icon, "border-emerald-100", "border-gray-200"],
    [icon, "text-emerald-600", "text-gray-400"],
  ]) {
    element.classList.toggle(yes, online);
    element.classList.toggle(no, !online);
  }
  status.lastChild.textContent = online ? "在线" : "离线";
}
async function refreshSummaries() {
  if (summaryBusy || document.hidden) return;
  summaryBusy = true;
  const generation = summaryGeneration;
  const list = [...devices];
  const roles = new Map();
  let cursor = 0;
  const worker = async () => {
    while (cursor < list.length) {
      const d = list[cursor++];
      await Promise.allSettled([
        (async () => {
          try {
            const response = await api(
              "/v1/call/group/web/device/" + encodeURIComponent(d.device_id),
            );
            if (response.code !== 200) throw Error();
            if (generation !== summaryGeneration) return;
            d.room = response.data;
            const card = cardFor(d.device_id);
            if (!card) return;
            if (typeof d.room.online === "boolean") {
              d.online = d.room.online;
              updateOnlineStatus(card, d.online);
            }
            const text = DevicePresentation.roomSummary(d.room);
            card.querySelector("[data-room]").textContent = text;
            card.querySelector("[data-room]").title = text;
            card.querySelector("[data-room-dot]").hidden =
              d.room.desired_state !== "joined";
          } catch {
            if (generation === summaryGeneration) {
              const card = cardFor(d.device_id);
              if (card) {
                card.querySelector("[data-room]").textContent =
                  "状态获取失败，请刷新";
              }
            }
          }
        })(),
        (async () => {
          let text = "角色获取失败，请刷新";
          try {
            const binding = await api(
              "/v1/ai/device/" + encodeURIComponent(d.device_id) + "/role",
            );
            if (binding.code !== 200) throw Error();
            let role = null;
            if (binding.data.role_id || binding.data.default_role_id) {
              const path = binding.data.role_id
                ? "/v1/ai/roles/" + encodeURIComponent(binding.data.role_id)
                : "/v1/ai/roles/default";
              if (!roles.has(path)) roles.set(path, api(path));
              const detail = await roles.get(path);
              if (detail.code === 200) role = detail.data;
              else if (![403, 404, 40300, 40400].includes(detail.code))
                throw Error();
            }
            text = DevicePresentation.roleSummary(binding.data, role);
          } catch {}
          if (generation === summaryGeneration) {
            const card = cardFor(d.device_id);
            if (card) {
              card.querySelector("[data-role]").textContent = text;
              card.querySelector("[data-role]").title = text;
            }
          }
        })(),
      ]);
    }
  };
  try {
    await Promise.all(Array.from({ length: Math.min(4, list.length) }, worker));
  } finally {
    summaryBusy = false;
    if (generation !== summaryGeneration) refreshSummaries();
  }
}
function goRoom(id) {
  location.href = "/v1/call/group/page?device_id=" + encodeURIComponent(id);
}
let selectedDeviceID = null;
let menuTrigger = null;
function closeDeviceMenu(restoreFocus = false) {
  $("device-menu").hidden = true;
  if (menuTrigger) {
    menuTrigger.setAttribute("aria-expanded", "false");
    if (restoreFocus) menuTrigger.focus();
  }
  menuTrigger = null;
}
function openDeviceMenu(id, trigger) {
  if (menuTrigger === trigger && !$("device-menu").hidden) {
    closeDeviceMenu(true);
    return;
  }
  closeDeviceMenu();
  if (!devices.some((d) => d.device_id === id)) return;
  selectedDeviceID = id;
  menuTrigger = trigger;
  const menu = $("device-menu");
  menu.hidden = false;
  trigger.setAttribute("aria-expanded", "true");
  const rect = trigger.getBoundingClientRect();
  const x = Math.max(8, Math.min(rect.right - menu.offsetWidth, innerWidth - menu.offsetWidth - 8));
  const y = rect.bottom + menu.offsetHeight + 8 < innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - menu.offsetHeight - 6);
  menu.style.left = x + "px";
  menu.style.top = y + "px";
  menu.querySelector("button").focus();
}
document.addEventListener("pointerdown", (event) => {
  if (!$("device-menu").contains(event.target) && !menuTrigger?.contains(event.target)) closeDeviceMenu();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !$("device-menu").hidden) closeDeviceMenu(true);
});
$("device-menu").addEventListener("keydown", (event) => {
  if (event.key === "Tab") { closeDeviceMenu(true); return; }
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const items = [...$("device-menu").querySelectorAll("button")];
  const index = items.indexOf(document.activeElement);
  const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[next].focus();
});
addEventListener("resize", () => closeDeviceMenu());
addEventListener("scroll", () => closeDeviceMenu(), true);
function detailList(rows) {
  return (
    '<dl class="device-details">' +
    rows
      .map(
        ([label, value]) =>
          `<dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd>`,
      )
      .join("") +
    "</dl>"
  );
}
function showMedia(scene) {
  const d = devices.find((d) => d.device_id === selectedDeviceID);
  if (!d) return;
  document.querySelectorAll("[data-scene]").forEach((b) => {
    b.setAttribute("aria-selected", String(b.dataset.scene === scene));
    b.tabIndex = b.dataset.scene === scene ? 0 : -1;
  });
  $("media-details").setAttribute("aria-labelledby", "tab-" + scene);
  const rows = DevicePresentation.mediaRows(d.profiles, scene);
  $("media-details").innerHTML = rows
    ? detailList(rows)
    : '<p class="detail-muted">该场景能力未上报</p>';
}
function openDeviceInfo() {
  const d = devices.find((d) => d.device_id === selectedDeviceID);
  if (!d) return;
  $("device-details").innerHTML = [
    ["设备名称", d.device_name || "未命名设备"],
    ["设备 ID", d.device_id],
    ["MAC", d.mac || "未上报"],
    ["绑定时间", d.bind_time || "未上报"],
  ]
    .map(([k, v]) => `<dt>${escapeHTML(k)}</dt><dd>${escapeHTML(v)}</dd>`)
    .join("");
  showMedia("stream");
  $("device-info").showModal();
}
$("device-menu").addEventListener("click", (event) => {
  const action = event.target.dataset.menuAction;
  if (!action) return;
  closeDeviceMenu(true);
  if (action === "rename") openNameModal(selectedDeviceID);
  else if (action === "info") openDeviceInfo();
  else if (action === "unbind") askReset(selectedDeviceID);
});
$("info-close").onclick = () => $("device-info").close();
document.querySelectorAll("[data-scene]").forEach((b, index) => {
  b.onclick = () => showMedia(b.dataset.scene);
  b.onkeydown = (e) => {
    if (!["ArrowLeft", "ArrowRight"].includes(e.key)) return;
    e.preventDefault();
    const tabs = [...document.querySelectorAll("[data-scene]")];
    const next = tabs[(index + (e.key === "ArrowRight" ? 1 : 2)) % 3];
    showMedia(next.dataset.scene);
    next.focus();
  };
});
for (const id of ["device-info"]) {
  $(id).addEventListener("click", (e) => {
    if (e.target === $(id)) {
      const box = $(id).getBoundingClientRect();
      if (
        e.clientX < box.left ||
        e.clientX > box.right ||
        e.clientY < box.top ||
        e.clientY > box.bottom
      )
        $(id).close();
    }
  });
}

function goLive(id) {
  location.href = "/player?device_id=" + encodeURIComponent(id);
}
function goConfig(id) {
  location.href = "/v1/ai/agent?device_id=" + encodeURIComponent(id);
}
function goContacts(id) {
  location.href = "/contacts?device_id=" + encodeURIComponent(id);
}

function updateNameCharCount() {
  const length = Array.from($("device-name-input").value || "").length;
  $("name-char-count").textContent = `${length} / 13`;
  $("name-char-count").classList.toggle("text-red-500", length > 13);
}

function openNameModal(deviceID) {
  const device = devices.find((item) => item.device_id === deviceID);
  if (!device) {
    toast("未找到该设备，请刷新后重试", true);
    return;
  }
  editingDeviceID = deviceID;
  $("device-name-input").value = device.device_name || "";
  $("name-device-id").textContent = deviceID;
  updateNameCharCount();
  const modal = $("name-modal");
  modal.classList.remove("hidden");
  modal.classList.add("flex");
  setTimeout(() => $("device-name-input").focus(), 0);
}

function closeNameModal() {
  editingDeviceID = null;
  const modal = $("name-modal");
  modal.classList.add("hidden");
  modal.classList.remove("flex");
  $("name-save-button").disabled = false;
  $("name-save-button").textContent = "保存名称";
}

async function saveDeviceName() {
  const deviceID = editingDeviceID;
  const deviceName = $("device-name-input").value.trim();
  if (!deviceID) return;
  if (!deviceName) {
    toast("请输入设备名称", true);
    $("device-name-input").focus();
    return;
  }
  if (Array.from(deviceName).length > 13) {
    toast("设备名称最多 13 个字符", true);
    $("device-name-input").focus();
    return;
  }

  const button = $("name-save-button");
  button.disabled = true;
  button.textContent = "保存中…";
  try {
    const data = await api("/v1/user/device/name", {
      method: "PUT",
      body: JSON.stringify({ device_id: deviceID, device_name: deviceName }),
    });
    if (data.code !== 200) throw new Error(data.msg || "保存失败");
    closeNameModal();
    toast("设备名称已保存");
    await loadDevices();
  } catch (error) {
    button.disabled = false;
    button.textContent = "保存名称";
    toast(error.message || "保存失败", true);
  }
}

function askReset(deviceID) {
  resetDeviceID = deviceID;
  const modal = $("reset-modal");
  modal.classList.remove("hidden");
  modal.classList.add("flex");
}

function closeModal() {
  resetDeviceID = null;
  const modal = $("reset-modal");
  modal.classList.add("hidden");
  modal.classList.remove("flex");
}

async function doReset() {
  const id = resetDeviceID;
  if (!id) return;
  closeModal();
  try {
    const data = await api("/v1/user/device/reset", {
      method: "DELETE",
      body: JSON.stringify({ device_id: id }),
    });
    if (data.code !== 200) throw Error(data.msg || "解绑失败");
    toast("设备已解绑");
    await loadDevices();
  } catch (error) {
    toast(error.message || "解绑失败，请重试", true);
  }
}

function logout() {
  localStorage.clear();
  sessionStorage.clear();
  location.href = "/login";
}

$("device-name-input").addEventListener("input", updateNameCharCount);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !$("name-modal").classList.contains("hidden"))
    closeNameModal();
});
loadDevices();

addEventListener("pagehide", () => {
  summaryGeneration++;
});
