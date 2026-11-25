(() => {
  "use strict";
  const key = "ccm2_appearance";
  const choices = [["system", "System"], ["light", "Light"], ["dark", "Dark"]];
  const sharedHost = ["ccm.hoanganhdev.com", "sso.hoanganhdev.com"].includes(location.hostname);
  const root = document.documentElement;
  let mode = "system";

  function storedMode() {
    try {
      const entry = document.cookie.split("; ").find(cookie => cookie.startsWith(key + "="));
      const value = entry?.slice(key.length + 1);
      return choices.some(([id]) => id === value) ? value : null;
    } catch {
      return null;
    }
  }

  function apply(value) {
    mode = value;
    root.dataset.appearance = value;
    document.querySelectorAll("[data-appearance-picker] input").forEach(input => {
      input.checked = input.value === value;
    });
  }

  function synchronize() {
    const value = storedMode();
    if (value && value !== mode) apply(value);
  }

  // Run in the head so even the first paint uses the saved preference.
  apply(storedMode() || "system");
  document.addEventListener("DOMContentLoaded", () => {
    document.querySelectorAll("[data-appearance-picker]").forEach((picker, index) => {
      const group = document.createElement("div");
      group.className = "appearance-options";
      for (const [value, text] of choices) {
        const label = document.createElement("label");
        const input = document.createElement("input");
        input.type = "radio";
        input.name = "appearance-" + index;
        input.value = value;
        input.checked = mode === value;
        const caption = document.createElement("span");
        caption.textContent = text;
        label.append(input, caption);
        group.append(label);
      }
      picker.addEventListener("change", event => {
        const value = event.target.value;
        if (!choices.some(([id]) => id === value)) return;
        apply(value);
        try {
          const domain = sharedHost && location.protocol === "https:" ? "; Domain=hoanganhdev.com" : "";
          const secure = location.protocol === "https:" ? "; Secure" : "";
          document.cookie = key + "=" + value + "; Path=/; Max-Age=31536000; SameSite=Lax" + domain + secure;
        } catch {
          // The selection still applies to this page when browser storage is blocked.
        }
      });
      picker.append(group);
      picker.hidden = false;
    });
  });
  window.addEventListener("pageshow", synchronize);
  window.addEventListener("focus", synchronize);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) synchronize(); });
})();
