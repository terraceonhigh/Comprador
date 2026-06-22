/* Comprador: shared page behaviour.
   Included synchronously in <head> so the backdrop + Fun Mode state are set
   before first paint (no flash); the toggle binding waits for the DOM.
   The backdrop pool is baked here because a static page can't list a folder, so
   regenerate this array when files in images/backdrops/ change. */
(function () {
  var backdrops = [
    "images/backdrops/pdia-1de63329-8e80-495d-9589-0d66c3d4afb9.jpg",
    "images/backdrops/pdia-2ced97c7-4cfd-47c7-85a3-aa03d14124dc.jpg",
    "images/backdrops/pdia-5b147ab6-6a07-4595-8acd-01f0120cfa67.jpg",
    "images/backdrops/pdia-833f13c1-9e65-4577-aa25-77bf7291199f.jpg",
    "images/backdrops/pdia-c81e576a-9cba-43b1-90cf-836d27e06730.jpg",
    "images/backdrops/pdia-cc7ec694-7ad0-4426-80a1-5ce7b5381d1c.jpg",
    "images/backdrops/pdia-e1745bcb-4f13-4992-b764-b77278c304b7.jpg",
    "images/backdrops/pdia-f454ebc9-7849-4dae-a8f3-451723807b00.jpg",
    "images/backdrops/pdia-fdd4be5e-993f-47e5-8e05-100cbbdb574c.jpg"
  ];
  var pick = backdrops[Math.floor(Math.random() * backdrops.length)];
  document.documentElement.style.setProperty("--backdrop", 'url("' + pick + '")');
  try { if (localStorage.getItem("comprador-fun") === "1") document.documentElement.classList.add("fun"); } catch (e) {}

  document.addEventListener("DOMContentLoaded", function () {
    var btn = document.getElementById("funToggle");
    if (!btn) return;
    var root = document.documentElement;
    function paint() {
      var on = root.classList.contains("fun");
      btn.setAttribute("aria-pressed", on ? "true" : "false");
      btn.textContent = on ? "Fun mode ✓" : "Fun mode";
    }
    btn.addEventListener("click", function () {
      var on = root.classList.toggle("fun");
      try { localStorage.setItem("comprador-fun", on ? "1" : "0"); } catch (e) {}
      paint();
    });
    paint();
  });
})();
