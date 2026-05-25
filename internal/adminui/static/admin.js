document.addEventListener("DOMContentLoaded", function () {
  var storageKey = "scrap.admin.ui.tweaks";
  var allowedAccents = ["#0093d0", "#0ea5e9", "#6366f1", "#34d399"];
  var defaults = {
    refreshSeconds: 20,
    compactNumbers: true,
    sparklines: true,
    accent: "#0093d0",
  };

  var settings = readSettings(storageKey, defaults, allowedAccents);

  wireClock();
  wireTweaks(storageKey, defaults, allowedAccents, settings);
  wireAutoRefresh(settings);
});

function wireClock() {
  var clock = document.getElementById("admin-clock");
  if (!clock) return;

  function tick() {
    clock.textContent = new Date().toISOString().replace("T", " ").slice(0, 19);
  }

  tick();
  window.setInterval(tick, 1000);
}

function wireTweaks(storageKey, defaults, allowedAccents, settings) {
  var toggle = document.querySelector("[data-tweaks-toggle]");
  var close = document.querySelector("[data-tweaks-close]");
  var panel = document.querySelector("[data-tweaks-panel]");
  var refresh = document.querySelector("[data-tweak-refresh]");
  var refreshOutput = document.querySelector("[data-tweak-refresh-output]");
  var compact = document.querySelector("[data-tweak-compact]");
  var sparklines = document.querySelector("[data-tweak-sparklines]");
  var accents = Array.prototype.slice.call(document.querySelectorAll("[data-tweak-accent]"));

  applySettings(settings, refresh, refreshOutput, compact, sparklines, accents);

  function save(next) {
    settings = sanitizeSettings(next, defaults, allowedAccents);
    writeSettings(storageKey, settings);
    applySettings(settings, refresh, refreshOutput, compact, sparklines, accents);
    window.dispatchEvent(new CustomEvent("scrap:tweaks-changed", { detail: settings }));
  }

  if (toggle && panel) {
    toggle.addEventListener("click", function () {
      var isHidden = panel.hasAttribute("hidden");
      if (isHidden) {
        panel.removeAttribute("hidden");
      } else {
        panel.setAttribute("hidden", "");
      }
      toggle.setAttribute("aria-expanded", String(isHidden));
    });
  }

  if (close && panel && toggle) {
    close.addEventListener("click", function () {
      panel.setAttribute("hidden", "");
      toggle.setAttribute("aria-expanded", "false");
      toggle.focus();
    });
  }

  if (refresh) {
    refresh.addEventListener("input", function () {
      var seconds = Number(refresh.value);
      if (!Number.isFinite(seconds)) seconds = defaults.refreshSeconds;
      save({
        refreshSeconds: seconds,
        compactNumbers: settings.compactNumbers,
        sparklines: settings.sparklines,
        accent: settings.accent,
      });
    });
  }

  if (compact) {
    compact.addEventListener("change", function () {
      save({
        refreshSeconds: settings.refreshSeconds,
        compactNumbers: compact.checked,
        sparklines: settings.sparklines,
        accent: settings.accent,
      });
    });
  }

  if (sparklines) {
    sparklines.addEventListener("change", function () {
      save({
        refreshSeconds: settings.refreshSeconds,
        compactNumbers: settings.compactNumbers,
        sparklines: sparklines.checked,
        accent: settings.accent,
      });
    });
  }

  accents.forEach(function (button) {
    button.addEventListener("click", function () {
      save({
        refreshSeconds: settings.refreshSeconds,
        compactNumbers: settings.compactNumbers,
        sparklines: settings.sparklines,
        accent: button.getAttribute("data-tweak-accent") || defaults.accent,
      });
    });
  });

  document.body.addEventListener("htmx:afterSwap", function () {
    applySettings(settings, refresh, refreshOutput, compact, sparklines, accents);
  });
}

function wireAutoRefresh(settings) {
  var timer = null;
  var currentSettings = settings;

  function restart(event) {
    if (event && event.detail) currentSettings = event.detail;
    if (timer) window.clearInterval(timer);
    if (!currentSettings.refreshSeconds || currentSettings.refreshSeconds < 5) return;
    timer = window.setInterval(refreshCurrentView, currentSettings.refreshSeconds * 1000);
  }

  window.addEventListener("scrap:tweaks-changed", restart);
  restart();
}

function refreshCurrentView() {
  if (document.hidden || !window.htmx) return;

  var path = window.location.pathname;
  var search = window.location.search;
  var refreshURL = "";

  if (path === "/admin/" || path === "/admin") {
    refreshURL = "/admin/views/overview";
  } else if (path.indexOf("/admin/views/") === 0) {
    refreshURL = path + search;
  }

  if (!refreshURL) return;
  window.htmx.ajax("GET", refreshURL, { target: "#main-content", swap: "innerHTML" });
}

function readSettings(storageKey, defaults, allowedAccents) {
  try {
    var raw = window.localStorage.getItem(storageKey);
    if (!raw) return sanitizeSettings(defaults, defaults, allowedAccents);
    return sanitizeSettings(Object.assign({}, defaults, JSON.parse(raw)), defaults, allowedAccents);
  } catch (_) {
    return Object.assign({}, defaults);
  }
}

function writeSettings(storageKey, settings) {
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(settings));
  } catch (_) {
    return;
  }
}

function sanitizeSettings(candidate, defaults, allowedAccents) {
  var refreshSeconds = Number(candidate.refreshSeconds);
  if (!Number.isFinite(refreshSeconds)) refreshSeconds = defaults.refreshSeconds;
  refreshSeconds = Math.max(5, Math.min(120, Math.round(refreshSeconds / 5) * 5));

  var accent = candidate.accent;
  if (allowedAccents.indexOf(accent) === -1) {
    accent = defaults.accent;
  }

  return {
    refreshSeconds: refreshSeconds,
    compactNumbers: Boolean(candidate.compactNumbers),
    sparklines: Boolean(candidate.sparklines),
    accent: accent,
  };
}

function applySettings(settings, refresh, refreshOutput, compact, sparklines, accents) {
  document.documentElement.style.setProperty("--pb-primary", settings.accent);
  document.documentElement.style.setProperty("--pb-ring", settings.accent);
  document.body.dataset.compactNumbers = String(settings.compactNumbers);
  document.body.dataset.sparklines = String(settings.sparklines);
  applyCompactNumbers(settings.compactNumbers);

  if (refresh) refresh.value = String(settings.refreshSeconds);
  if (refreshOutput) refreshOutput.textContent = String(settings.refreshSeconds) + "s";
  if (compact) compact.checked = settings.compactNumbers;
  if (sparklines) sparklines.checked = settings.sparklines;

  accents.forEach(function (button) {
    button.classList.toggle("active", button.getAttribute("data-tweak-accent") === settings.accent);
  });
}

function applyCompactNumbers(compactNumbers) {
  var values = document.querySelectorAll("[data-full-value][data-compact-value]");
  Array.prototype.forEach.call(values, function (element) {
    var full = element.getAttribute("data-full-value");
    var compact = element.getAttribute("data-compact-value");
    element.textContent = compactNumbers ? compact : full;
  });
}
