-- WoW Touch Sync
-- 1. Phone keys: every action the phone uses that has no key in game gets a spare key through an
--    override binding. Override bindings live only for this session and never change your saved key bindings.
-- 2. Snapshot: on logout or /reload the character's name, class, keys and action bar spells are saved to
--    WTF/Account/<account>/<realm>/<character>/SavedVariables/WoWTouchSync.lua, which the Mac app reads.

local ACTIONS = {
  "MOVEFORWARD", "MOVEBACKWARD", "TURNLEFT", "TURNRIGHT", "STRAFELEFT", "STRAFERIGHT", "JUMP", "SITORSTAND",
  "STARTAUTORUN", "TOGGLEAUTORUN", "INTERACTTARGET", "TARGETNEARESTENEMY", "OPENCHAT", "TOGGLEGAMEMENU",
  "TOGGLEWORLDMAP", "TOGGLEBACKPACK", "OPENALLBAGS", "TOGGLECHARACTER0", "TOGGLESPELLBOOK", "TOGGLETALENTS",
  "TOGGLEQUESTLOG", "NAMEPLATES", "TOGGLEUI",
}
for i = 1, 12 do
  ACTIONS[#ACTIONS + 1] = "ACTIONBUTTON" .. i
  ACTIONS[#ACTIONS + 1] = "MULTIACTIONBAR1BUTTON" .. i
end
for i = 1, 10 do
  ACTIONS[#ACTIONS + 1] = "SHAPESHIFTBUTTON" .. i
  ACTIONS[#ACTIONS + 1] = "BONUSACTIONBUTTON" .. i
end

-- Keys nobody types on a MacBook. No modifiers, so macro conditions like [mod:shift] still behave.
local POOL = {
  "NUMPAD0", "NUMPAD1", "NUMPAD2", "NUMPAD3", "NUMPAD4", "NUMPAD5", "NUMPAD6", "NUMPAD7", "NUMPAD8", "NUMPAD9",
  "NUMPADDIVIDE", "NUMPADMULTIPLY", "NUMPADMINUS", "NUMPADPLUS", "NUMPADDECIMAL",
  "F13", "F14", "F15", "F16", "F17", "F18", "F19", "HOME", "END", "PAGEUP", "PAGEDOWN",
  "CTRL-NUMPAD0", "CTRL-NUMPAD1", "CTRL-NUMPAD2", "CTRL-NUMPAD3", "CTRL-NUMPAD4", "CTRL-NUMPAD5",
  "CTRL-NUMPAD6", "CTRL-NUMPAD7", "CTRL-NUMPAD8", "CTRL-NUMPAD9", "CTRL-F13", "CTRL-F14", "CTRL-F15",
}

-- the frame that shows each action bar button, used to find which spell sits on it
local BUTTON_FRAME = {
  ACTIONBUTTON = "ActionButton", MULTIACTIONBAR1BUTTON = "MultiBarBottomLeftButton",
}

local owner = CreateFrame("Frame")

-- Camera button for the phone. Left click: snap the camera to saved view 5 (behind the character, facing where the
-- character faces). Right click: save the current camera angle and zoom as view 5. Calling SetView twice skips the
-- smooth camera blend so the snap is instant. Until a view is saved, view 5 is reset to the game's standard view.
local camera = CreateFrame("Button", "WoWTouchCamera", UIParent)
camera:RegisterForClicks("AnyUp", "AnyDown")
camera:SetScript("OnClick", function(_, button, down)
  if down == false then return end
  if button == "RightButton" then
    SaveView(5)
    WoWTouchSyncCam = true
    print("WoW Touch: camera angle saved. The camera button now returns to it.")
  else
    if not WoWTouchSyncCam then ResetView(5) end
    SetView(5)
    SetView(5)
  end
end)
-- pseudo actions the phone asks for; they click the camera button instead of running a game binding
local CLICKS = { WOWTOUCH_CAMRESET = "LeftButton", WOWTOUCH_CAMSAVE = "RightButton" }
local phoneKeys = {} -- action -> spare key assigned this session
local busy, pending, lastAssign = false, false, 0

local function assign()
  if InCombatLockdown() then pending = true return end
  busy = true
  ClearOverrideBindings(owner)
  wipe(phoneKeys)
  local free = {}
  for _, key in ipairs(POOL) do
    local act = GetBindingAction(key)
    if not act or act == "" then free[#free + 1] = key end
  end
  local n = 1
  for _, action in ipairs({ "WOWTOUCH_CAMRESET", "WOWTOUCH_CAMSAVE" }) do
    if free[n] then
      SetOverrideBindingClick(owner, false, free[n], "WoWTouchCamera", CLICKS[action])
      phoneKeys[action] = free[n]
      n = n + 1
    end
  end
  for _, action in ipairs(ACTIONS) do
    if not GetBindingKey(action) and free[n] then
      SetOverrideBinding(owner, false, free[n], action)
      phoneKeys[action] = free[n]
      n = n + 1
    end
  end
  busy, pending, lastAssign = false, false, GetTime()
end

local function spellOn(slot)
  if not slot or not HasAction(slot) then return nil end
  local kind, id = GetActionInfo(slot)
  local name
  if kind == "spell" and id then
    name = (C_Spell and C_Spell.GetSpellName and C_Spell.GetSpellName(id)) or (GetSpellInfo and GetSpellInfo(id))
  elseif kind == "item" and id then
    name = (C_Item and C_Item.GetItemNameByID and C_Item.GetItemNameByID(id)) or (GetItemInfo and GetItemInfo(id))
  elseif kind == "macro" then
    name = GetActionText(slot)
  end
  return name or GetActionText(slot)
end

local function snapshot()
  local out = {}
  local name, realm = UnitFullName("player")
  local _, class = UnitClass("player")
  out[#out + 1] = "char " .. (name or "?") .. "-" .. (realm or GetRealmName() or "?")
  out[#out + 1] = "class " .. (class or "?")
  out[#out + 1] = "time " .. time()
  for _, action in ipairs({ "WOWTOUCH_CAMRESET", "WOWTOUCH_CAMSAVE" }) do
    if phoneKeys[action] then out[#out + 1] = "phone " .. action .. " " .. phoneKeys[action] end
  end
  for _, action in ipairs(ACTIONS) do
    local keys = { GetBindingKey(action) }
    for _, k in ipairs(keys) do out[#out + 1] = "bind " .. action .. " " .. k end
    if phoneKeys[action] then out[#out + 1] = "phone " .. action .. " " .. phoneKeys[action] end
  end
  for prefix, frame in pairs(BUTTON_FRAME) do
    for i = 1, 12 do
      local btn = _G[frame .. i]
      local slot = btn and (btn.action or (btn.GetAttribute and btn:GetAttribute("action")))
      if prefix == "ACTIONBUTTON" and not slot then slot = i end
      local spell = spellOn(slot)
      if spell then out[#out + 1] = "spell " .. prefix .. i .. " " .. spell end
    end
  end
  for i = 1, 10 do
    if GetShapeshiftFormInfo then
      local _, active, castable, spellID = GetShapeshiftFormInfo(i)
      if spellID then
        local s = (C_Spell and C_Spell.GetSpellName and C_Spell.GetSpellName(spellID)) or (GetSpellInfo and GetSpellInfo(spellID))
        if s then out[#out + 1] = "spell SHAPESHIFTBUTTON" .. i .. " " .. s end
      end
    end
  end
  WoWTouchSyncChar = out
end

owner:RegisterEvent("PLAYER_LOGIN")
owner:RegisterEvent("UPDATE_BINDINGS")
owner:RegisterEvent("PLAYER_REGEN_ENABLED")
owner:RegisterEvent("PLAYER_LOGOUT")
owner:SetScript("OnEvent", function(_, event)
  if event == "PLAYER_LOGOUT" then snapshot() return end
  if event == "PLAYER_REGEN_ENABLED" and not pending then return end
  -- our own SetOverrideBinding calls fire UPDATE_BINDINGS too; ignore those echoes
  if busy or (event == "UPDATE_BINDINGS" and GetTime() - lastAssign < 1) then return end
  assign()
end)

SLASH_WOWTOUCHSYNC1 = "/wts"
SlashCmdList.WOWTOUCHSYNC = function()
  local n = 0
  for _ in pairs(phoneKeys) do n = n + 1 end
  print(("WoW Touch: %d phone-only keys active. Type /reload to send your keys and spells to the phone."):format(n))
end
