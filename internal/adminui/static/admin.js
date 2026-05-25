document.addEventListener("DOMContentLoaded", function () {
  var clock = document.getElementById("admin-clock");
  if (!clock) return;
  function tick() {
    clock.textContent = new Date().toISOString().replace("T", " ").slice(0, 19);
  }
  tick();
  window.setInterval(tick, 1000);
});
