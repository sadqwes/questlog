'use strict';
(function () {
  const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря'];
  const DOW = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];
  const DOW_FULL = ['Понедельник', 'Вторник', 'Среда', 'Четверг', 'Пятница', 'Суббота', 'Воскресенье'];
  const PERKS = { 2: 'наушники полиглота', 3: 'плащ странницы', 5: 'корону кластера' };
  const PAGES = ['today', 'journey', 'arena', 'food', 'library', 'me'];
  const MEAL_KINDS = [['breakfast', 'Завтрак'], ['lunch', 'Обед'], ['dinner', 'Ужин'], ['snack', 'Перекус'], ['drink', 'Напиток']];
  const REVIEW_FIELDS = [
    ['good', 'Что получилось?', 'даже маленькое'],
    ['hard', 'Что было тяжело?', 'без самокритики, просто факт'],
    ['change', 'Что поменяю на следующей неделе?', 'например: English утром, а не вечером'],
    ['story', 'История для собеса: ситуация → действие → результат', 'например: настроила бэкап Postgres в MinIO и проверила восстановление'],
    ['book', 'Где я в книге', 'глава / страница'],
    ['weight', 'Вес (по желанию)', 'можно пропустить'],
  ];

  const $ = id => document.getElementById(id);
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const pad = n => String(n).padStart(2, '0');
  const localKey = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const parseDay = s => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d); };
  const human = s => { const d = parseDay(s); return `${d.getDate()} ${MONTHS[d.getMonth()]}`; };
  const short = s => { const d = parseDay(s); return `${d.getDate()} ${MONTHS[d.getMonth()].slice(0, 3)}`; };
  const shiftDay = (s, n) => { const d = parseDay(s); d.setDate(d.getDate() + n); return localKey(d); };
  const dowOf = s => (parseDay(s).getDay() + 6) % 7;
  const pct = b => Math.max(0, Math.min(100, (b.xp - b.from) / Math.max(1, b.to - b.from) * 100));
  const kindName = k => (MEAL_KINDS.find(x => x[0] === k) || [k, k])[1];

  let S = null;           // состояние с сервера
  let page = 'today';
  let sel = null;         // выбранный день похода (индекс в календаре)
  let foodDay = localKey(new Date());
  let openGuide = null;
  let pendingFiles = [];  // фото, выбранные в форме питания, ещё не отправленные
  let busy = false;       // идёт отправка — фоновое обновление не трогает страницу
  const drafts = {};
  const opened = {};
  let lastJSON = '';
  const detailsOpen = (key, byDefault) => (key in opened ? opened[key] : byDefault) ? ' open' : '';

  // ---------- API ----------
  async function api(method, path, body, isForm) {
    const opts = { method, headers: {} };
    if (isForm) opts.body = body;
    else if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    const r = await fetch(path, opts);
    if (r.status === 401) { location.href = '/login'; throw new Error('Сессия закончилась — войди снова'); }
    if (!r.ok) {
      let msg = 'Ошибка ' + r.status;
      try { msg = (await r.json()).error || msg; } catch (e) { /* не JSON */ }
      throw new Error(msg);
    }
    return r.status === 204 ? null : r.json();
  }

  async function load() {
    try {
      const next = await api('GET', '/api/state');
      showErr('');
      const json = JSON.stringify(next);
      if (json === lastJSON) return;
      lastJSON = json;
      S = next;
      if (sel == null) sel = homeIdx();
      render();
    } catch (e) {
      showErr('Не получилось загрузить прогресс: ' + e.message);
    }
  }

  async function mutate(method, path, body, label) {
    const before = S.stats;
    try {
      const res = await api(method, path, body);
      S = res && res.stats ? res : await api('GET', '/api/state');
      lastJSON = JSON.stringify(S);
      showErr('');
      render();
      celebrate(before, S.stats, label);
      return true;
    } catch (e) {
      toast(e.message);
      return false;
    }
  }

  function showErr(msg) { $('err').textContent = msg; $('err').hidden = !msg; }

  function homeIdx() {
    const cal = S.calendar, today = localKey(new Date());
    const i = cal.findIndex(d => d.date === today);
    if (i >= 0 && !cal[i].pre) return i;
    return today > cal[cal.length - 1].date ? cal.length - 1 : cal.findIndex(d => !d.pre);
  }

  // ---------- роутер ----------
  function route() {
    const p = (location.hash.match(/^#\/(\w+)/) || [])[1];
    page = PAGES.includes(p) ? p : 'today';
    document.querySelectorAll('[data-page]').forEach(s => { s.hidden = s.dataset.page !== page; });
    document.querySelectorAll('.nav a').forEach(a => {
      if (a.getAttribute('href') === '#/' + page) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
    if (S) render();
    window.scrollTo(0, 0);
  }

  // ---------- отрисовка ----------
  function render() {
    renderTop();
    ({ today: () => { renderQuests(); renderTodayFood(); }, journey: () => { renderMap(); renderBosses(); },
      arena: renderArena, food: renderFood, library: renderShelf, me: () => { renderHero(); renderAch(); } })[page]();
    document.querySelectorAll('[data-w]').forEach(el => { el.style.width = el.dataset.w + '%'; });
  }

  function renderTop() {
    const h = S.stats.hero;
    $('topName').textContent = S.progress.hero || 'Героиня';
    $('topLvl').textContent = 'УР ' + h.level;
    $('topXp').dataset.w = pct(h);
    drawSprite($('spriteSmall'), h.level);
    const sub = document.querySelector('[data-page="journey"] .sub');
    sub.textContent = `${human(S.plan.start)} — ${human(S.calendar[S.calendar.length - 1].date)}`;
  }

  function renderHero() {
    const h = S.stats.hero;
    $('xpBar').dataset.w = pct(h);
    $('xpText').textContent = h.xp + ' XP';
    $('xpNext').textContent = `до уровня ${h.level + 1}: ${h.to - h.xp} XP`;
    if (document.activeElement !== $('heroName')) $('heroName').value = S.progress.hero || 'Героиня';
    $('heroClass').textContent = `Класс: DevOps-странница · уровень ${h.level}`;
    $('skills').innerHTML = S.plan.skills.map(s => {
      const b = S.stats.skills[s.id];
      return `<div class="skill c-${s.id}"><span class="nm"><b>${esc(s.name)}</b><small>${esc(s.sub)} · ${b.xp} XP</small></span>
        <div class="bar"><i data-w="${pct(b)}"></i></div><span class="lv">ур ${b.level}</span></div>`;
    }).join('');
    drawSprite($('sprite'), h.level);
  }

  function sayLine(day, marks, meta) {
    const n = S.plan.skills.filter(s => marks[s.id]).length;
    const today = localKey(new Date());
    if (meta.rest) return 'Привал. Отдых восстанавливает силы — +5 XP просто за честный отдых.';
    if (day.weekend) return n ? 'Бонус в таверне засчитан. Совсем не обязательно, но приятно.' : 'Таверна. Играй спокойно — поход подождёт до понедельника.';
    if (n === S.plan.skills.length) return 'Все квесты дня. Легендарный день.';
    if (n) return `${n} из ${S.plan.skills.length} — опыт уже начислен. Минимум тоже победа.`;
    if (day.date < today) return 'Этот день прошёл без квестов — бывает. Опыт не сгорает, идём дальше.';
    return 'Выбери, что успеешь. Можно начать с одного минимума.';
  }

  function renderQuests() {
    const day = S.calendar[sel], week = S.plan.weeks[day.row];
    const marks = S.progress.marks[day.date] || {}, meta = S.progress.days[day.date] || {}, guides = S.progress.guides[day.date] || {};
    let h = `<div class="day-head"><div><div class="label">${day.row ? `Неделя ${day.row} · ` : ''}${esc(week.title)}${day.weekend ? ' · таверна' : ''}</div>
      <h1>${DOW_FULL[day.dow]}, ${human(day.date)}</h1></div>
      <div class="day-nav"><button class="btn" data-nav="-1" aria-label="Предыдущий день">←</button><button class="btn" data-nav="0">Сегодня</button><button class="btn" data-nav="1" aria-label="Следующий день">→</button></div></div>
      <p class="say">${esc(sayLine(day, marks, meta))}</p>`;
    if (day.weekend) {
      h += `<div class="tavern"><b>Бонусные квесты по желанию</b><ul><li>Сценарий на Killercoda минут на 20 или маленькая задачка в лабе</li><li>Переключить язык в игре на английский — это тоже красноречие</li><li>Пара страниц Kubernetes in Action</li><li>Долгая прогулка или фильм по теме недели</li></ul></div>`;
    }
    h += '<div class="quests">' + S.plan.skills.map(s => {
      const q = (day.quests || []).find(x => x.skill === s.id);
      const v = marks[s.id] || 0, g = guides[s.id], open = openGuide === s.id && g;
      let body = '<span class="muted">Любое дело по навыку — бонус.</span>';
      if (q) {
        body = `<span>${q.tag ? `<span class="tag">${esc(q.tag)}</span>` : ''}${mdInline(q.text)}</span>
          <span class="min"><b>Минимум:</b> ${mdInline(q.min)}</span>` +
          (g ? `<button class="ask" data-guide="${s.id}" aria-expanded="${!!open}">${open ? 'Свернуть разбор' : 'Разбор наставника'}</button>`
            : `<span class="hint">Разбора пока нет <button class="copy" data-copy="${esc(`Разбери квест «${s.name}» на ${human(day.date)} в questlog`)}">попросить в Claude Code</button></span>`);
      }
      return `<div class="quest s${v} c-${s.id}"><span class="glyph" aria-hidden="true">${esc(s.glyph)}</span>
        <div class="qt"><span class="sk">${esc(s.name)} · ${esc(s.sub)}</span>${body}</div>
        <div class="acts"><button class="act" data-skill="${s.id}" data-v="1" aria-pressed="${v === 1}">минимум +10</button>
        <button class="act" data-skill="${s.id}" data-v="2" aria-pressed="${v === 2}">полностью +25</button></div>
        ${open ? `<div class="mentor"><div class="guide">${md(g.content)}</div></div>` : ''}</div>`;
    }).join('') + '</div>';
    const note = drafts['note:' + day.date] != null ? drafts['note:' + day.date] : (meta.note || '');
    h += `<div class="dayfoot"><label class="field" for="note">Запись в дневнике: что узнала или как себя чувствую
      <input type="text" id="note" value="${esc(note)}" placeholder="например: наконец-то поняла headless service" maxlength="2000"></label>
      <button class="btn" id="rest" aria-pressed="${!!meta.rest}">${meta.rest ? 'Привал взят ✓' : 'Сделать привал'}</button></div>`;
    $('quests').innerHTML = h;
  }

  function mealsOf(day) { return S.progress.meals.filter(m => m.day === day); }

  function renderTodayFood() {
    const day = S.calendar[sel].date, meals = mealsOf(day);
    const prot = meals.filter(m => m.protein).length, veg = meals.filter(m => m.veggies).length;
    $('todayFood').innerHTML = `<div class="spread"><h2>Питание · ${human(day)}</h2><a class="btn" href="#/food" data-food-day="${day}">Открыть дневник</a></div>
      ${meals.length
        ? `<div class="row"><span class="chip">${meals.length} ${plural(meals.length, 'запись', 'записи', 'записей')}</span>
           <span class="chip ${prot ? 'ok' : ''}">белок: ${prot}</span><span class="chip ${veg ? 'ok' : ''}">овощи: ${veg}</span></div>`
        : '<p class="muted">Записей пока нет. Отправь фото еды мне в чат или добавь запись в дневнике.</p>'}
      ${waterRow(day)}`;
  }

  function plural(n, one, few, many) {
    const m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return one;
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
    return many;
  }

  // ---------- питание ----------
  function renderFood() {
    const meals = mealsOf(foodDay), fdd = S.progress.foodDays[foodDay] || {}, comment = fdd.comment;
    const today = localKey(new Date());
    let h = `<div class="day-head"><div><div class="label">${DOW_FULL[dowOf(foodDay)]}</div><h1>${human(foodDay)}</h1></div>
      <div class="day-nav"><button class="btn" data-fnav="-1" aria-label="Предыдущий день">←</button><button class="btn" data-fnav="0">Сегодня</button>
      <button class="btn" data-fnav="1" aria-label="Следующий день"${foodDay >= today ? ' disabled' : ''}>→</button></div></div>`;
    h += waterRow(foodDay);
    if (comment) h += `<div class="comment"><span class="label">Наставник о дне</span><div class="guide">${md(comment)}</div></div>`;
    h += meals.length ? '<div class="meals">' + meals.map(mealCard).join('') + '</div>'
      : '<p class="muted">В этот день записей нет — и это нормально. Можно добавить запись ниже или прислать фото мне в чат.</p>';
    h += `<div class="hint">Хочешь мягкий разбор дня? <button class="copy" data-copy="${esc(`Разбери мой день питания за ${human(foodDay)} в questlog`)}">попросить в Claude Code</button></div>`;
    $('foodDay').innerHTML = h;
    renderFoodAdd();
    renderFoodWeek();
  }

  const GLASS_GOAL = 8; // 8 стаканов по 250 мл ≈ 2 литра

  function waterRow(day) {
    const n = (S.progress.foodDays[day] || {}).water || 0;
    const cups = Array.from({ length: Math.max(GLASS_GOAL, n) }, (_, i) => `<span class="glass${i < n ? ' full' : ''}"></span>`).join('');
    return `<div class="water"><b>Вода</b><span class="glasses" aria-label="${n} из ${GLASS_GOAL} стаканов">${cups}</span>
      <span class="muted small">${n} ${plural(n, 'стакан', 'стакана', 'стаканов')} · ≈${(n * 0.25).toFixed(2).replace(/\.?0+$/, '')} л</span>
      <span class="row"><button class="btn" data-water="${esc(day)}" data-dw="-1" aria-label="Убрать стакан"${n ? '' : ' disabled'}>−</button>
      <button class="btn primary" data-water="${esc(day)}" data-dw="1">+ стакан</button></span></div>`;
  }

  function mealEditForm(m) {
    return `<div class="meal-edit">
      <div class="form-grid">
        <label class="field">Что это<select data-edit-kind>${MEAL_KINDS.map(([k, n]) => `<option value="${k}"${m.kind === k ? ' selected' : ''}>${n}</option>`).join('')}</select></label>
        <label class="field">Время<input type="time" data-edit-at value="${esc(m.at)}"></label>
      </div>
      <label class="field">Описание<textarea data-edit-desc rows="3">${esc(m.description)}</textarea></label>
      <div class="row"><button class="btn primary" data-save-meal="${m.id}">Сохранить</button><button class="btn" data-cancel-edit="${m.id}">Отмена</button></div></div>`;
  }

  function mealCard(m) {
    const confirm = drafts['del:' + m.id], editing = drafts['edit:' + m.id];
    const toggle = (field, on, yes, no) => `<button class="chip btnchip ${on ? 'ok' : ''}" data-toggle="${field}" data-meal="${m.id}" aria-pressed="${on}" title="Нажми, чтобы переключить">${on ? yes : no}</button>`;
    return `<article class="meal"><div class="meal-head"><span class="meal-kind">${esc(kindName(m.kind))}${m.at ? ` · <span class="muted">${esc(m.at)}</span>` : ''}</span>
      <span class="row">${toggle('protein', m.protein, 'белок ✓', 'без белка')}${toggle('veggies', m.veggies, 'овощи ✓', 'без овощей')}</span></div>
      ${editing ? mealEditForm(m) : `<p>${esc(m.description)}</p>`}
      ${m.photos.length ? `<div class="photos">${m.photos.map((k, i) => `<button data-photo="${esc(k)}" data-meal="${m.id}" data-idx="${i}" aria-label="Открыть фото ${i + 1} из ${m.photos.length}"><img src="/api/photos/${esc(k)}" loading="lazy" alt="${esc(m.description)}"></button>`).join('')}</div>` : ''}
      ${m.comment ? `<div class="comment"><span class="label">Наставник</span><div class="guide">${md(m.comment)}</div></div>` : ''}
      <div class="row">${editing ? '' : `<button class="btn" data-edit-meal="${m.id}">Изменить</button>`}<button class="btn danger" data-del-meal="${m.id}">${confirm ? 'Точно удалить?' : 'Удалить'}</button></div></article>`;
  }

  function defaultKind() {
    const h = new Date().getHours();
    return h < 11 ? 'breakfast' : h < 16 ? 'lunch' : h < 21 ? 'dinner' : 'snack';
  }

  function renderFoodAdd() {
    if (busy || document.activeElement && $('foodAdd').contains(document.activeElement)) return; // не мешаем, пока заполняют
    const d = drafts.meal || {};
    const now = new Date();
    $('foodAdd').innerHTML = `<h2>Добавить запись</h2>
      <form id="mealForm" class="stack">
        <div class="form-grid">
          <label class="field" for="mKind">Что это<select id="mKind">${MEAL_KINDS.map(([k, n]) => `<option value="${k}"${(d.kind || defaultKind()) === k ? ' selected' : ''}>${n}</option>`).join('')}</select></label>
          <label class="field" for="mAt">Время<input type="time" id="mAt" value="${esc(d.at || `${pad(now.getHours())}:${pad(now.getMinutes())}`)}"></label>
        </div>
        <label class="field" for="mDesc">Что ела или пила<textarea id="mDesc" rows="3" placeholder="например: ролл с копчёной грудкой, перцем и морковью по-корейски">${esc(d.desc || '')}</textarea></label>
        <div class="row">
          <label class="check"><input type="checkbox" id="mProt"${d.prot ? ' checked' : ''}><span>есть белок</span></label>
          <label class="check"><input type="checkbox" id="mVeg"${d.veg ? ' checked' : ''}><span>есть овощи</span></label>
        </div>
        <label class="drop" for="mPhotos">📷 Добавить фото<input type="file" id="mPhotos" accept="image/*" multiple></label>
        <div class="previews" id="mPreviews"></div>
        <div class="row"><button class="btn primary" type="submit">Сохранить в дневник · +5 XP</button><span class="muted small">запись будет в дне: ${human(foodDay)}</span></div>
      </form>`;
    showPreviews();
  }

  function showPreviews() {
    const box = $('mPreviews');
    if (!box) return;
    box.innerHTML = '';
    pendingFiles.forEach(f => {
      const img = document.createElement('img');
      img.alt = f.name;
      img.src = URL.createObjectURL(f);
      img.onload = () => URL.revokeObjectURL(img.src);
      box.appendChild(img);
    });
  }

  function renderFoodWeek() {
    const days = [];
    for (let i = 6; i >= 0; i--) days.push(shiftDay(foodDay, -i));
    const all = days.flatMap(mealsOf);
    const prot = all.filter(m => m.protein).length, veg = all.filter(m => m.veggies).length;
    $('foodWeek').innerHTML = `<h2>Последние 7 дней</h2>
      <div class="week">${days.map(d => {
        const n = mealsOf(d).length;
        return `<button class="wday${n ? ' has' : ''}${d === foodDay ? ' sel' : ''}" data-food-day="${d}"><span class="muted">${DOW[dowOf(d)]}</span><b>${parseDay(d).getDate()}</b><span>${n || '·'}</span></button>`;
      }).join('')}</div>
      ${all.length ? `<div class="row"><span class="chip">${all.length} ${plural(all.length, 'запись', 'записи', 'записей')}</span>
        <span class="chip ok">с белком: ${prot} из ${all.length}</span><span class="chip ok">с овощами: ${veg} из ${all.length}</span></div>
        <p class="muted small">Цель не «идеально», а чуть больше белка и овощей, чем на прошлой неделе.</p>`
      : '<p class="muted small">Здесь появится картина недели, когда будут записи.</p>'}`;
  }

  // уменьшаем фото в браузере: быстрее грузится с телефона. Сервер всё равно пересожмёт и уберёт EXIF.
  async function shrinkPhoto(file) {
    try {
      const bmp = await createImageBitmap(file); // учитывает поворот из EXIF
      const scale = Math.min(1, 1600 / Math.max(bmp.width, bmp.height));
      const c = document.createElement('canvas');
      c.width = Math.round(bmp.width * scale); c.height = Math.round(bmp.height * scale);
      c.getContext('2d').drawImage(bmp, 0, 0, c.width, c.height);
      return await new Promise(res => c.toBlob(b => res(b || file), 'image/jpeg', 0.85));
    } catch (e) {
      return file; // формат не понял браузер — отправим как есть, сервер скажет, если что
    }
  }

  async function saveMeal(e) {
    e.preventDefault();
    const desc = $('mDesc').value.trim();
    if (!desc) { toast('Напиши хотя бы пару слов, что это было'); return; }
    busy = true;
    const btn = e.target.querySelector('[type=submit]');
    btn.disabled = true; btn.textContent = 'Сохраняю…';
    const before = S.stats;
    try {
      const meal = await api('POST', '/api/meals', {
        day: foodDay, at: $('mAt').value, kind: $('mKind').value, description: desc,
        protein: $('mProt').checked, veggies: $('mVeg').checked,
      });
      for (const f of pendingFiles) {
        const fd = new FormData();
        fd.append('photo', await shrinkPhoto(f), 'photo.jpg');
        await api('POST', `/api/meals/${meal.id}/photos`, fd, true);
      }
      pendingFiles = []; delete drafts.meal;
      busy = false;
      document.activeElement.blur();
      S = await api('GET', '/api/state'); lastJSON = JSON.stringify(S);
      render();
      celebrate(before, S.stats, 'дневник питания');
    } catch (err) {
      busy = false;
      btn.disabled = false; btn.textContent = 'Сохранить в дневник · +5 XP';
      toast(err.message);
    }
  }

  // ---------- арена ----------
  const status = a => a.feedback != null ? 'reviewed' : a.answer != null ? 'answered' : 'new';

  function renderArena() {
    const arena = S.progress.arena;
    const chips = a => `<span class="row"><span class="chip">${esc(a.kind === 'mock' ? 'пробное собеседование' : a.topic)}</span>${a.lang === 'en' ? '<span class="chip">EN</span>' : ''}</span>`;
    const fresh = arena.filter(a => status(a) === 'new');
    $('arenaNew').innerHTML = fresh.length ? fresh.map(a => `<div class="aitem">${chips(a)}<p class="qtext">${esc(a.question)}</p>
      <textarea rows="5" data-aid="${a.id}" placeholder="${a.lang === 'en' ? 'Answer in English — short and simple is fine. Можно вставлять русские слова.' : 'Ответь своими словами, как на собеседовании'}">${esc(drafts['a:' + a.id] || '')}</textarea>
      <div class="row"><button class="btn primary" data-answer="${a.id}">Ответить</button><button class="btn" data-dunno="${a.id}">Не знаю — разберём</button></div></div>`).join('')
      : '<p class="muted">Новых вопросов нет. Попроси в Claude Code задать вопросы — они появятся здесь.</p>';

    const wait = arena.filter(a => status(a) === 'answered');
    $('arenaWait').innerHTML = wait.length ? wait.map(a => `<div class="aitem wait">${chips(a)}<p class="qtext">${esc(a.question)}</p><p class="answer">${esc(a.answer)}</p></div>`).join('') +
      '<p class="hint">Когда будет удобно: <button class="copy" data-copy="Разбери мои ответы на арене questlog">скопировать «разбери арену»</button></p>'
      : '<p class="muted">Всё разобрано.</p>';

    $('tstats').innerHTML = S.plan.arenaTopics.map(t => {
      const x = S.stats.topics[t] || { count: 0, avg: 0 };
      return `<div class="tstat"><span>${esc(t)}</span><span class="muted">${x.count ? `${x.avg.toFixed(1)} · ${x.count} вопр.` : 'ещё не было'}</span><div class="bar"><i data-w="${x.avg / 5 * 100}"></i></div></div>`;
    }).join('');

    const done = arena.filter(a => status(a) === 'reviewed').slice(0, 30);
    $('hist').innerHTML = done.length ? done.map(a => `<details data-key="a:${a.id}"${detailsOpen('a:' + a.id, false)}><summary>${a.score != null ? `<span class="score">${a.score}/5</span> · ` : ''}${esc(a.kind === 'mock' ? 'Пробное собеседование · ' + (a.createdAt || '').slice(0, 10) : a.topic + ' — ' + a.question)}</summary>
      <div class="guide">${a.kind === 'q' ? `<p class="answer">${esc(a.answer)}</p>` : ''}${md(a.feedback)}</div></details>`).join('')
      : '<p class="muted">Здесь будут разобранные вопросы — к ним удобно возвращаться перед живым собеседованием.</p>';
  }

  // ---------- поход ----------
  function renderMap() {
    const today = localKey(new Date());
    let h = '<span></span>' + DOW.map(x => `<div class="dow">${x}</div>`).join('');
    S.calendar.forEach((d, i) => {
      if (d.dow === 0) h += `<div class="wkl">${d.row ? 'нед ' + d.row : 'пролог'}</div>`;
      const marks = S.progress.marks[d.date] || {}, meta = S.progress.days[d.date] || {};
      const cl = ['tile'];
      if (d.pre) cl.push('pre');
      if (d.weekend) cl.push('we');
      if (i === sel) cl.push('sel');
      if (meta.rest) cl.push('rest');
      const tg = meta.rest ? 'привал' : d.weekend ? 'таверна' : d.chapter ? 'глава ' + d.chapter : '';
      h += `<button class="${cl.join(' ')}" data-i="${i}"${d.pre ? ' disabled' : ''} aria-label="${human(d.date)}">
        <span class="d">${parseDay(d.date).getDate()}${d.date === today ? '<em>●</em>' : ''}</span><span class="tg">${tg}</span>
        <span class="pips">${S.plan.skills.map(s => `<i class="pip s${marks[s.id] || 0} c-${s.id}"></i>`).join('')}</span></button>`;
    });
    $('map').innerHTML = h;
  }

  function renderBosses() {
    const curRow = S.calendar[homeIdx()].row;
    $('bosses').innerHTML = S.plan.weeks.map((w, wi) => {
      const items = S.progress.weekItems[w.id] || {}, b = S.stats.bosses[w.id], r = S.progress.reviews[w.id] || {};
      const days = S.calendar.filter(d => d.row === wi && !d.pre);
      const li = (text, key, xp) => `<li><label class="check${items[key] ? ' done' : ''}"><input type="checkbox" data-week="${esc(w.id)}" data-item="${esc(key)}"${items[key] ? ' checked' : ''}><span>${esc(text)} <span class="muted small">+${xp}</span></span></label></li>`;
      const field = ([f, label, ph]) => {
        const v = drafts[`r:${w.id}:${f}`] != null ? drafts[`r:${w.id}:${f}`] : (r[f] || '');
        const id = `r-${w.id}-${f}`;
        return `<label for="${id}">${label}` + (f === 'book' || f === 'weight'
          ? `<input type="text" id="${esc(id)}" data-rw="${esc(w.id)}" data-rf="${esc(f)}" value="${esc(v)}" placeholder="${ph}">`
          : `<textarea id="${esc(id)}" data-rw="${esc(w.id)}" data-rf="${esc(f)}" placeholder="${ph}">${esc(v)}</textarea>`) + '</label>';
      };
      return `<article class="card boss${wi ? '' : ' wide'}${wi === curRow ? ' cur' : ''}${b.down ? ' dead' : ''}">
        <div><div class="label">${wi ? 'Неделя ' + wi : 'Пролог'} · ${short(days[0].date)} — ${short(days[days.length - 1].date)}</div>
        <h3 class="bn">${esc(w.boss)}</h3><p class="muted small">${esc(w.bossDesc)}</p></div>
        <div class="hp"><div class="bar"><i data-w="${b.left / b.total * 100}"></i></div><small>${b.down ? 'повержен · опыт получен' : `HP ${b.left}/${b.total}`}</small></div>
        <div><div class="label">Удары по CKA · ${esc(w.title)}</div><ul class="checks">${w.topics.map((t, i) => li(t, 't' + i, 15)).join('')}</ul></div>
        <div><div class="label">Удары из лабы</div><ul class="checks">${w.lab.map((t, i) => li(t, 'l' + i, 30)).join('')}</ul></div>
        <div class="focus"><div><span>Английский</span><span>${esc(w.eng)}</span></div><div><span>Тело</span><span>${esc(w.body)}</span></div><div><span>Кругозор</span><span>${esc(w.mind)}</span></div></div>
        ${chronicle(w.id, REVIEW_FIELDS.map(field).join(''), !!(r.good || r.hard || r.change))}</article>`;
    }).join('');
  }

  // Хроника недели: fieldsHTML уже собран из экранированных значений (esc) в renderBosses.
  function chronicle(weekID, fieldsHTML, filled) {
    const key = 'r:' + weekID;
    return '<details data-key="' + esc(key) + '"' + detailsOpen(key, filled) + '>' +
      '<summary>Хроника недели · пятница, 10 минут · +20 XP</summary>' +
      '<div class="review">' + fieldsHTML + '</div></details>';
  }

  // ---------- книги, достижения ----------
  function renderShelf() {
    const read = S.progress.chapters, done = S.plan.readChapters;
    const next = S.plan.chapters.find(c => c.n > done && !read[c.n]);
    $('shelf').innerHTML = S.plan.chapters.map(c => {
      if (c.n <= done) return `<span class="spine old" title="Глава ${c.n} — прочитана"><span class="n">${c.n}</span><span class="t">${esc(c.title)}</span></span>`;
      const cl = 'spine' + (read[c.n] ? ' read' : '') + (next && next.n === c.n ? ' next' : '');
      return `<button class="${cl}" data-ch="${c.n}" aria-pressed="${!!read[c.n]}" title="Глава ${c.n}: ${esc(c.title)}"><span class="n">${c.n}</span><span class="t">${esc(c.title)}</span></button>`;
    }).join('');
  }

  function renderAch() {
    $('ach').innerHTML = S.stats.achievements.map(a => `<div class="badge${a.got ? ' got' : ''}"><span class="ic">${esc(a.icon)}</span>
      <span><b>${esc(a.name)}</b><small>${a.got ? 'Получено · ' : ''}${esc(a.desc)}</small></span></div>`).join('');
  }

  // ---------- персонаж ----------
  const BASE = [
    '................', '.....hhhhhh.....', '....hhhhhhhh....', '...hhhsssshhh...',
    '...hhsesseshh...', '...hhssmmsshh...', '...hh..ss..hh...', '...hhcccccchh...',
    '...sccccccccs...', '...scccwwcccs...', '...sccccccccs...', '....pppppppp....',
    '....ppp..ppp....', '....ppp..ppp....', '....bbb..bbb....', '................',
  ];
  const PAL = { h: '#9C7FE3', s: '#F6D2B8', e: '#3A3452', m: '#E27C9A', c: '#8FB3F5', w: '#FFFFFF', p: '#6F6A99', b: '#B08968', a: '#F3A6C8', r: '#F29BB4', g: '#F6C85F' };
  function drawSprite(canvas, level) {
    const x = canvas.getContext('2d');
    x.clearRect(0, 0, 16, 16);
    const put = (cx, cy, col) => { x.fillStyle = col; x.fillRect(cx, cy, 1, 1); };
    if (level >= 3) for (let y = 7; y <= 13; y++) { put(2, y, PAL.r); put(13, y, PAL.r); }
    BASE.forEach((row, y) => [...row].forEach((ch, cx) => { if (PAL[ch]) put(cx, y, PAL[ch]); }));
    if (level >= 2) [[2, 3], [2, 4], [2, 5], [13, 3], [13, 4], [13, 5], [3, 2], [12, 2]].forEach(([a, b]) => put(a, b, PAL.a));
    if (level >= 5) { [5, 7, 8, 10].forEach(cx => put(cx, 0, PAL.g)); for (let cx = 5; cx <= 10; cx++) put(cx, 1, PAL.g); }
  }

  // ---------- мелочи ----------
  let toastT;
  function toast(msg) {
    const t = $('toast');
    t.textContent = msg; t.hidden = false;
    clearTimeout(toastT);
    toastT = setTimeout(() => { t.hidden = true; }, 2800);
  }
  function celebrate(b, a, label) {
    if (!b) return;
    if (a.hero.level > b.hero.level) { toast(`Уровень ${a.hero.level}!` + (PERKS[a.hero.level] ? ` Персонаж получил ${PERKS[a.hero.level]}.` : '')); return; }
    const got = a.achievements.find(x => x.got && !(b.achievements.find(y => y.id === x.id) || {}).got);
    if (got) { toast('Достижение: ' + got.name); return; }
    if (a.hero.xp > b.hero.xp) toast(`+${a.hero.xp - b.hero.xp} XP` + (label ? ' · ' + label : ''));
  }
  async function copy(text) {
    try { await navigator.clipboard.writeText(text); }
    catch (e) {
      const ta = document.createElement('textarea');
      ta.value = text; document.body.appendChild(ta); ta.select();
      document.execCommand('copy'); ta.remove();
    }
    toast('Скопировано — вставь в Claude Code');
  }
  const skillName = id => (S.plan.skills.find(s => s.id === id) || {}).name || id;

  // ---------- события ----------
  document.addEventListener('click', e => {
    const t = e.target;
    const lbBtn = t.closest('[data-lb]');
    if (lbBtn) { lbBtn.dataset.lb === 'close' ? closeGallery() : stepGallery(+lbBtn.dataset.lb); return; }
    if (t.id === 'lightbox') { closeGallery(); return; } // клик по тёмному фону
    const ph = t.closest('[data-photo]');
    if (ph) {
      const meal = S.progress.meals.find(m => String(m.id) === ph.dataset.meal);
      openGallery(meal ? meal.photos : [ph.dataset.photo], +ph.dataset.idx || 0);
      return;
    }
    const wbtn = t.closest('[data-water]');
    if (wbtn) {
      const day = wbtn.dataset.water, n = ((S.progress.foodDays[day] || {}).water || 0) + (+wbtn.dataset.dw);
      mutate('PUT', `/api/food-days/${day}/water`, { glasses: Math.max(0, n) }, 'вода');
      return;
    }
    const tg = t.closest('[data-toggle]');
    if (tg) {
      const m = S.progress.meals.find(x => String(x.id) === tg.dataset.meal);
      if (m) mutate('PATCH', '/api/meals/' + m.id, { [tg.dataset.toggle]: !m[tg.dataset.toggle] });
      return;
    }
    const ed = t.closest('[data-edit-meal]');
    if (ed) { drafts['edit:' + ed.dataset.editMeal] = true; render(); return; }
    const ce = t.closest('[data-cancel-edit]');
    if (ce) { delete drafts['edit:' + ce.dataset.cancelEdit]; render(); return; }
    const sv = t.closest('[data-save-meal]');
    if (sv) {
      const box = sv.closest('.meal-edit'), id = sv.dataset.saveMeal;
      const description = box.querySelector('[data-edit-desc]').value.trim();
      if (!description) { toast('Описание не может быть пустым'); return; }
      mutate('PATCH', '/api/meals/' + id, {
        kind: box.querySelector('[data-edit-kind]').value, at: box.querySelector('[data-edit-at]').value, description,
      }).then(ok => { if (ok) { delete drafts['edit:' + id]; render(); } });
      return;
    }
    const tile = t.closest('.tile');
    if (tile) { sel = +tile.dataset.i; openGuide = null; location.hash = '#/today'; return; }
    const fd = t.closest('[data-food-day]');
    if (fd) { foodDay = fd.dataset.foodDay; if (page === 'food') render(); return; }
    const nav = t.closest('[data-nav]');
    if (nav) {
      const v = +nav.dataset.nav, first = S.calendar.findIndex(d => !d.pre);
      sel = v === 0 ? homeIdx() : Math.min(S.calendar.length - 1, Math.max(first, sel + v));
      openGuide = null; render(); return;
    }
    const fnav = t.closest('[data-fnav]');
    if (fnav) {
      const v = +fnav.dataset.fnav, today = localKey(new Date());
      foodDay = v === 0 ? today : shiftDay(foodDay, v);
      if (foodDay > today) foodDay = today;
      render(); return;
    }
    const act = t.closest('.act');
    if (act) {
      const day = S.calendar[sel], skill = act.dataset.skill, v = +act.dataset.v;
      const cur = (S.progress.marks[day.date] || {})[skill] || 0;
      mutate('PUT', `/api/marks/${day.date}/${skill}`, { level: cur === v ? 0 : v }, skillName(skill));
      return;
    }
    if (t.closest('#rest')) {
      const day = S.calendar[sel], meta = S.progress.days[day.date] || {};
      mutate('PATCH', '/api/days/' + day.date, { rest: !meta.rest }, 'привал');
      return;
    }
    const g = t.closest('[data-guide]');
    if (g) { openGuide = openGuide === g.dataset.guide ? null : g.dataset.guide; renderQuests(); return; }
    const cp = t.closest('[data-copy]');
    if (cp) { copy(cp.dataset.copy); return; }
    const ch = t.closest('[data-ch]');
    if (ch) { const n = +ch.dataset.ch; mutate('PUT', '/api/chapters/' + n, { read: !S.progress.chapters[n] }, 'глава ' + n); return; }
    const del = t.closest('[data-del-meal]');
    if (del) {
      const id = del.dataset.delMeal;
      if (!drafts['del:' + id]) {
        drafts['del:' + id] = true; render();
        setTimeout(() => { delete drafts['del:' + id]; if (page === 'food') render(); }, 4000);
        return;
      }
      delete drafts['del:' + id];
      mutate('DELETE', '/api/meals/' + id);
      return;
    }
    const ans = t.closest('[data-answer]');
    if (ans) {
      const id = ans.dataset.answer, v = (drafts['a:' + id] || '').trim();
      if (!v) { toast('Напиши хоть пару слов — или нажми «Не знаю»'); return; }
      mutate('PATCH', '/api/arena/' + id, { answer: v }).then(ok => { if (ok) { delete drafts['a:' + id]; toast('Ответ сохранён — попроси разбор в Claude Code'); } });
      return;
    }
    const dn = t.closest('[data-dunno]');
    if (dn) mutate('PATCH', '/api/arena/' + dn.dataset.dunno, { answer: 'Не знаю' }).then(ok => { if (ok) toast('Это нормально — разберём тему с нуля'); });
  });

  // ---------- галерея фото ----------
  const gallery = { photos: [], idx: 0 };
  function openGallery(photos, idx) {
    gallery.photos = photos; gallery.idx = idx;
    showGallery();
    $('lightbox').hidden = false;
  }
  function showGallery() {
    const n = gallery.photos.length;
    $('lightboxImg').src = '/api/photos/' + gallery.photos[gallery.idx];
    $('lightboxCount').textContent = n > 1 ? `${gallery.idx + 1} / ${n}` : '';
    document.querySelectorAll('.lb-prev,.lb-next').forEach(b => { b.hidden = n < 2; });
  }
  function stepGallery(d) {
    const n = gallery.photos.length;
    if (n < 2) return;
    gallery.idx = (gallery.idx + d + n) % n; // по кругу: после последнего — снова первое
    showGallery();
  }
  function closeGallery() { $('lightbox').hidden = true; $('lightboxImg').removeAttribute('src'); }

  document.addEventListener('keydown', e => {
    if ($('lightbox').hidden) return;
    if (e.key === 'Escape') closeGallery();
    else if (e.key === 'ArrowRight') stepGallery(1);
    else if (e.key === 'ArrowLeft') stepGallery(-1);
  });
  // свайп на телефоне
  let touchX = null;
  $('lightbox').addEventListener('touchstart', e => { touchX = e.touches[0].clientX; }, { passive: true });
  $('lightbox').addEventListener('touchend', e => {
    if (touchX == null) return;
    const dx = e.changedTouches[0].clientX - touchX;
    touchX = null;
    if (Math.abs(dx) > 50) stepGallery(dx < 0 ? 1 : -1);
  });

  document.addEventListener('submit', e => { if (e.target.id === 'mealForm') saveMeal(e); });

  document.addEventListener('input', e => {
    const t = e.target;
    if (t.id === 'note') drafts['note:' + S.calendar[sel].date] = t.value;
    else if (t.dataset.aid) drafts['a:' + t.dataset.aid] = t.value;
    else if (t.dataset.rf) drafts[`r:${t.dataset.rw}:${t.dataset.rf}`] = t.value;
    else if (['mDesc', 'mAt', 'mKind', 'mProt', 'mVeg'].includes(t.id)) {
      drafts.meal = { desc: $('mDesc').value, at: $('mAt').value, kind: $('mKind').value, prot: $('mProt').checked, veg: $('mVeg').checked };
    }
  });

  document.addEventListener('change', e => {
    const t = e.target;
    if (t.id === 'mPhotos') { pendingFiles = pendingFiles.concat([...t.files]).slice(0, 6); t.value = ''; showPreviews(); return; }
    if (t.id === 'heroName') {
      const name = t.value.trim();
      if (name) mutate('PUT', '/api/hero', { name });
    } else if (t.id === 'note') {
      const date = S.calendar[sel].date;
      mutate('PATCH', '/api/days/' + date, { note: t.value }).then(() => { delete drafts['note:' + date]; });
    } else if (t.dataset.item) {
      mutate('PUT', `/api/weeks/${t.dataset.week}/items/${t.dataset.item}`, { done: t.checked }, 'удар по боссу');
    } else if (t.dataset.rf) {
      const week = t.dataset.rw, cur = S.progress.reviews[week] || {}, body = {};
      REVIEW_FIELDS.forEach(([f]) => { const k = `r:${week}:${f}`; body[f] = drafts[k] != null ? drafts[k] : (cur[f] || ''); });
      mutate('PUT', `/api/weeks/${week}/review`, body, 'хроника').then(() => { REVIEW_FIELDS.forEach(([f]) => { delete drafts[`r:${week}:${f}`]; }); });
    }
  });

  // toggle не всплывает — ловим на фазе перехвата
  document.addEventListener('toggle', e => {
    const d = e.target;
    if (d.matches && d.matches('details[data-key]')) opened[d.dataset.key] = d.open;
  }, true);

  // Наставник пишет разборы и записи о еде через API — подтягиваем их, пока вкладка открыта.
  const typing = () => { const a = document.activeElement; return a && (a.tagName === 'TEXTAREA' || a.tagName === 'INPUT' || a.tagName === 'SELECT'); };
  const editing = () => Object.keys(drafts).some(k => k.startsWith('edit:'));
  const quiet = () => S && !busy && !pendingFiles.length && !editing() && document.visibilityState === 'visible' && !typing();
  setInterval(() => { if (quiet()) load(); }, 30000);
  document.addEventListener('visibilitychange', () => { if (quiet()) load(); });

  window.addEventListener('hashchange', route);
  route();
  load();
})();
