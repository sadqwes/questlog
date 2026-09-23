'use strict';
(function () {
  const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря'];
  const DOW = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];
  const DOW_FULL = ['Понедельник', 'Вторник', 'Среда', 'Четверг', 'Пятница', 'Суббота', 'Воскресенье'];
  const PERKS = { 2: 'наушники полиглота', 3: 'плащ странницы', 5: 'корону кластера' };
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
  const localKey = d => d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  const parseDay = s => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d); };
  const human = s => { const d = parseDay(s); return d.getDate() + ' ' + MONTHS[d.getMonth()]; };
  const short = s => { const d = parseDay(s); return d.getDate() + ' ' + MONTHS[d.getMonth()].slice(0, 3); };
  const pct = b => Math.max(0, Math.min(100, (b.xp - b.from) / Math.max(1, b.to - b.from) * 100));

  let S = null;          // состояние с сервера: plan, calendar, progress, stats
  let sel = null;        // индекс выбранного дня в календаре
  let openGuide = null;  // навык, чей разбор раскрыт
  const drafts = {};     // недописанные тексты, чтобы перерисовка их не стирала

  // ---------- API ----------
  async function api(method, path, body) {
    const r = await fetch(path, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    });
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
      S = await api('GET', '/api/state');
      showErr('');
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
      showErr('');
      render();
      celebrate(before, S.stats, label);
    } catch (e) {
      toast(e.message);
    }
  }

  function showErr(msg) { $('err').textContent = msg; $('err').hidden = !msg; }

  function homeIdx() {
    const cal = S.calendar, today = localKey(new Date());
    const i = cal.findIndex(d => d.date === today);
    if (i >= 0 && !cal[i].pre) return i;
    return today > cal[cal.length - 1].date ? cal.length - 1 : cal.findIndex(d => !d.pre);
  }

  // ---------- render ----------
  function render() {
    renderHero();
    renderQuests();
    renderArena();
    renderMap();
    renderBosses();
    renderShelf();
    renderAch();
    document.querySelectorAll('[data-w]').forEach(el => { el.style.width = el.dataset.w + '%'; });
  }

  function renderHero() {
    const h = S.stats.hero;
    $('lvlBadge').textContent = 'УР ' + h.level;
    $('xpBar').dataset.w = pct(h);
    $('xpText').textContent = h.xp + ' XP';
    $('xpNext').textContent = 'до ур. ' + (h.level + 1) + ': ' + (h.to - h.xp);
    if (document.activeElement !== $('heroName')) $('heroName').value = S.progress.hero || 'Героиня';
    const cal = S.calendar;
    $('heroClass').textContent = 'Класс: DevOps-странница · поход ' + human(S.plan.start) + ' — ' + human(cal[cal.length - 1].date);
    $('skills').innerHTML = S.plan.skills.map(s => {
      const b = S.stats.skills[s.id];
      return '<div class="skill c-' + s.id + '"><span class="nm"><b>' + esc(s.name) + '</b><small>' + esc(s.sub) + ' · ' + b.xp + ' XP</small></span>' +
        '<div class="bar"><i data-w="' + pct(b) + '"></i></div><span class="lv">УР ' + b.level + '</span></div>';
    }).join('');
    drawSprite(h.level);
  }

  function sayLine(day, marks, meta) {
    const n = S.plan.skills.filter(s => marks[s.id]).length;
    const today = localKey(new Date());
    if (meta.rest) return 'Привал. Отдых восстанавливает силы — +5 XP просто за честный отдых.';
    if (day.weekend) return n ? 'Бонус в таверне засчитан. Совсем не обязательно, но приятно.' : 'Таверна. Играй спокойно — поход подождёт до понедельника.';
    if (n === S.plan.skills.length) return 'Все квесты дня. Легендарный день.';
    if (n) return n + ' из ' + S.plan.skills.length + ' — опыт уже начислен. Минимум тоже победа.';
    if (day.date < today) return 'Этот день прошёл без квестов — бывает. Опыт не сгорает, идём дальше.';
    return 'Выбери, что успеешь. Можно начать с одного минимума.';
  }

  function renderQuests() {
    const day = S.calendar[sel], week = S.plan.weeks[day.row];
    const marks = S.progress.marks[day.date] || {}, meta = S.progress.days[day.date] || {}, guides = S.progress.guides[day.date] || {};
    let h = '<div class="qhead"><div><div class="h">' + (day.row ? 'Неделя ' + day.row + ' · ' : '') + esc(week.title) + (day.weekend ? ' · таверна' : '') + '</div>' +
      '<h2>' + DOW_FULL[day.dow] + ', ' + human(day.date) + '</h2></div>' +
      '<div class="nav"><button class="btn" data-nav="-1" aria-label="Предыдущий день">←</button><button class="btn" data-nav="0">Сегодня</button><button class="btn" data-nav="1" aria-label="Следующий день">→</button></div></div>' +
      '<p class="say">' + esc(sayLine(day, marks, meta)) + '</p>';
    if (day.weekend) {
      h += '<div class="tavern"><b>Бонусные квесты по желанию</b><ul><li>Сценарий на Killercoda минут на 20 или маленькая задачка в лабе</li><li>Переключить язык в игре на английский — это тоже красноречие</li><li>Пара страниц Kubernetes in Action</li><li>Долгая прогулка или фильм по теме недели</li></ul></div>';
    }
    h += '<div class="quests">' + S.plan.skills.map(s => {
      const q = (day.quests || []).find(x => x.skill === s.id);
      const v = marks[s.id] || 0, g = guides[s.id], open = openGuide === s.id && g;
      let body = '<span class="muted">Любое дело по навыку — бонус.</span>';
      if (q) {
        body = '<span>' + (q.tag ? '<span class="tag">' + esc(q.tag) + '</span>' : '') + mdInline(q.text) + '</span>' +
          '<span class="min"><b>Минимум:</b> ' + mdInline(q.min) + '</span>' +
          (g ? '<span><button class="ask" data-guide="' + s.id + '" aria-expanded="' + !!open + '">' + (open ? 'Свернуть разбор' : 'Разбор наставника') + '</button></span>'
            : '<span class="hint">Разбора пока нет <button class="copy" data-copy="' + esc('Разбери квест «' + s.name + '» на ' + human(day.date) + ' в questlog') + '">попросить в Claude Code</button></span>');
      }
      return '<div class="quest s' + v + ' c-' + s.id + '"><span class="glyph" aria-hidden="true">' + esc(s.glyph) + '</span>' +
        '<div class="qt"><span class="sk">' + esc(s.name) + ' · ' + esc(s.sub) + '</span>' + body + '</div>' +
        '<div class="acts"><button class="act" data-skill="' + s.id + '" data-v="1" aria-pressed="' + (v === 1) + '">минимум +10</button>' +
        '<button class="act" data-skill="' + s.id + '" data-v="2" aria-pressed="' + (v === 2) + '">полностью +25</button></div>' +
        (open ? '<div class="mentor"><div class="guide">' + md(g.content) + '</div></div>' : '') + '</div>';
    }).join('') + '</div>';
    const note = drafts['note:' + day.date] != null ? drafts['note:' + day.date] : (meta.note || '');
    h += '<div class="dayfoot"><label for="note">Запись в дневнике: что узнала или как себя чувствую<input type="text" id="note" value="' + esc(note) + '" placeholder="например: наконец-то поняла headless service" maxlength="2000"></label>' +
      '<button class="btn" id="rest" aria-pressed="' + !!meta.rest + '">' + (meta.rest ? 'Привал взят ✓' : 'Сделать привал') + '</button></div>';
    $('quests').innerHTML = h;
  }

  function status(a) { return a.feedback != null ? 'reviewed' : a.answer != null ? 'answered' : 'new'; }

  function renderArena() {
    const arena = S.progress.arena;
    const chips = a => '<span class="row"><span class="chip">' + esc(a.kind === 'mock' ? 'пробное собеседование' : a.topic) + '</span>' + (a.lang === 'en' ? '<span class="chip">EN</span>' : '') + '</span>';
    const fresh = arena.filter(a => status(a) === 'new');
    $('arenaNew').innerHTML = fresh.length ? fresh.map(a => {
      const d = drafts['a:' + a.id] || '';
      return '<div class="aitem">' + chips(a) + '<p class="qtext">' + esc(a.question) + '</p>' +
        '<textarea rows="5" data-aid="' + a.id + '" placeholder="' + (a.lang === 'en' ? 'Answer in English — short and simple is fine. Можно вставлять русские слова.' : 'Ответь своими словами, как на собеседовании') + '">' + esc(d) + '</textarea>' +
        '<div class="row"><button class="btn primary" data-answer="' + a.id + '">Ответить</button><button class="btn" data-dunno="' + a.id + '">Не знаю — разберём</button></div></div>';
    }).join('') : '<p class="muted small">Новых вопросов нет. Попроси в Claude Code задать вопросы — они появятся здесь.</p>';

    const wait = arena.filter(a => status(a) === 'answered');
    $('arenaWait').innerHTML = wait.length ? wait.map(a =>
      '<div class="aitem wait">' + chips(a) + '<p class="qtext">' + esc(a.question) + '</p><p class="answer">' + esc(a.answer) + '</p></div>'
    ).join('') + '<p class="hint">Когда будет удобно: <button class="copy" data-copy="Разбери мои ответы на арене questlog">скопировать «разбери арену»</button></p>'
      : '<p class="muted small">Всё разобрано.</p>';

    $('tstats').innerHTML = S.plan.arenaTopics.map(t => {
      const x = S.stats.topics[t] || { count: 0, avg: 0 };
      return '<div class="tstat"><span>' + esc(t) + '</span><span>' + (x.count ? x.avg.toFixed(1) + ' · ' + x.count + ' вопр.' : 'ещё не было') + '</span><div class="bar"><i data-w="' + (x.avg / 5 * 100) + '"></i></div></div>';
    }).join('');

    const done = arena.filter(a => status(a) === 'reviewed').slice(0, 20);
    $('hist').innerHTML = done.length ? done.map(a =>
      '<details><summary>' + (a.score != null ? a.score + '/5 · ' : '') + esc(a.kind === 'mock' ? 'Пробное собеседование · ' + (a.createdAt || '').slice(0, 10) : a.topic + ' — ' + a.question) + '</summary>' +
      '<div class="guide">' + (a.kind === 'q' ? '<p class="answer">' + esc(a.answer) + '</p>' : '') + md(a.feedback) + '</div></details>'
    ).join('') : '<p class="muted small">Здесь будут разобранные вопросы — к ним удобно возвращаться перед живым собеседованием.</p>';
  }

  function renderMap() {
    const today = localKey(new Date());
    let h = '<span></span>' + DOW.map(x => '<div class="dow">' + x + '</div>').join('');
    S.calendar.forEach((d, i) => {
      if (d.dow === 0) h += '<div class="wkl">' + (d.row ? 'нед ' + d.row : 'пролог') + '</div>';
      const marks = S.progress.marks[d.date] || {}, meta = S.progress.days[d.date] || {};
      const cl = ['tile'];
      if (d.pre) cl.push('pre');
      if (d.weekend) cl.push('we');
      if (i === sel) cl.push('sel');
      if (meta.rest) cl.push('rest');
      const tg = meta.rest ? 'привал' : d.weekend ? 'таверна' : d.chapter ? 'глава ' + d.chapter : '';
      h += '<button class="' + cl.join(' ') + '" data-i="' + i + '"' + (d.pre ? ' disabled' : '') + ' aria-label="' + human(d.date) + '">' +
        '<span class="d">' + parseDay(d.date).getDate() + (d.date === today ? '<em>★</em>' : '') + '</span><span class="tg">' + tg + '</span>' +
        '<span class="pips">' + S.plan.skills.map(s => '<i class="pip s' + (marks[s.id] || 0) + ' c-' + s.id + '"></i>').join('') + '</span></button>';
    });
    $('map').innerHTML = h;
  }

  function renderBosses() {
    const curRow = S.calendar[homeIdx()].row;
    $('bosses').innerHTML = S.plan.weeks.map((w, wi) => {
      const items = S.progress.weekItems[w.id] || {}, b = S.stats.bosses[w.id], r = S.progress.reviews[w.id] || {};
      const days = S.calendar.filter(d => d.row === wi && !d.pre);
      const li = (text, key, xp) => '<li><label class="check' + (items[key] ? ' done' : '') + '"><input type="checkbox" data-week="' + w.id + '" data-item="' + key + '"' + (items[key] ? ' checked' : '') + '><span>' + esc(text) + ' <span class="muted small">+' + xp + '</span></span></label></li>';
      const field = ([f, label, ph]) => {
        const v = drafts['r:' + w.id + ':' + f] != null ? drafts['r:' + w.id + ':' + f] : (r[f] || '');
        const id = 'r-' + w.id + '-' + f;
        return '<label for="' + id + '">' + label + (f === 'book' || f === 'weight'
          ? '<input type="text" id="' + id + '" data-rw="' + w.id + '" data-rf="' + f + '" value="' + esc(v) + '" placeholder="' + ph + '">'
          : '<textarea id="' + id + '" data-rw="' + w.id + '" data-rf="' + f + '" placeholder="' + ph + '">' + esc(v) + '</textarea>') + '</label>';
      };
      return '<article class="panel boss' + (wi ? '' : ' wide') + (wi === curRow ? ' cur' : '') + (b.down ? ' dead' : '') + '">' +
        '<div><div class="h muted">' + (wi ? 'Неделя ' + wi : 'Пролог') + ' · ' + short(days[0].date) + ' — ' + short(days[days.length - 1].date) + '</div>' +
        '<h3 class="bn">' + esc(w.boss) + '</h3><p class="muted small">' + esc(w.bossDesc) + '</p></div>' +
        '<div class="hp"><div class="bar"><i data-w="' + (b.left / b.total * 100) + '"></i></div><small>' + (b.down ? 'повержен · опыт получен' : 'HP ' + b.left + '/' + b.total) + '</small></div>' +
        '<div><div class="sub">Удары по CKA · ' + esc(w.title) + '</div><ul class="checks">' + w.topics.map((t, i) => li(t, 't' + i, 15)).join('') + '</ul></div>' +
        '<div><div class="sub">Удары из лабы</div><ul class="checks">' + w.lab.map((t, i) => li(t, 'l' + i, 30)).join('') + '</ul></div>' +
        '<div class="focus"><div><span>Английский</span><span>' + esc(w.eng) + '</span></div><div><span>Тело</span><span>' + esc(w.body) + '</span></div><div><span>Кругозор</span><span>' + esc(w.mind) + '</span></div></div>' +
        '<details' + (r.good || r.hard || r.change ? ' open' : '') + '><summary>Хроника недели · пятница, 10 минут · +20 XP</summary><div class="review">' + REVIEW_FIELDS.map(field).join('') + '</div></details></article>';
    }).join('');
  }

  function renderShelf() {
    const read = S.progress.chapters, done = S.plan.readChapters;
    const next = S.plan.chapters.find(c => c.n > done && !read[c.n]);
    $('shelf').innerHTML = S.plan.chapters.map(c => {
      if (c.n <= done) return '<span class="spine old" title="Глава ' + c.n + ' — прочитана"><span class="n">' + c.n + '</span><span class="t">' + esc(c.title) + '</span></span>';
      const cl = 'spine' + (read[c.n] ? ' read' : '') + (next && next.n === c.n ? ' next' : '');
      return '<button class="' + cl + '" data-ch="' + c.n + '" aria-pressed="' + !!read[c.n] + '" title="Глава ' + c.n + ': ' + esc(c.title) + '"><span class="n">' + c.n + '</span><span class="t">' + esc(c.title) + '</span></button>';
    }).join('');
  }

  function renderAch() {
    $('ach').innerHTML = S.stats.achievements.map(a =>
      '<div class="badge' + (a.got ? ' got' : '') + '"><span class="ic">' + esc(a.icon) + '</span><span><b>' + esc(a.name) + '</b><small>' + (a.got ? 'Получено · ' : '') + esc(a.desc) + '</small></span></div>'
    ).join('');
  }

  // ---------- sprite ----------
  const BASE = [
    '................', '.....hhhhhh.....', '....hhhhhhhh....', '...hhhsssshhh...',
    '...hhsesseshh...', '...hhssmmsshh...', '...hh..ss..hh...', '...hhcccccchh...',
    '...sccccccccs...', '...scccwwcccs...', '...sccccccccs...', '....pppppppp....',
    '....ppp..ppp....', '....ppp..ppp....', '....bbb..bbb....', '................',
  ];
  const PAL = { h: '#8A5CD6', s: '#F2C6A0', e: '#1B1530', m: '#C9546B', c: '#7C94FF', w: '#FFFFFF', p: '#2F2A55', b: '#7A4B2A', a: '#E685DA', r: '#C94F6D', g: '#F4C152' };
  function drawSprite(level) {
    const x = $('sprite').getContext('2d');
    x.clearRect(0, 0, 16, 16);
    const put = (cx, cy, col) => { x.fillStyle = col; x.fillRect(cx, cy, 1, 1); };
    if (level >= 3) for (let y = 7; y <= 13; y++) { put(2, y, PAL.r); put(13, y, PAL.r); }
    BASE.forEach((row, y) => [...row].forEach((ch, cx) => { if (PAL[ch]) put(cx, y, PAL[ch]); }));
    if (level >= 2) [[2, 3], [2, 4], [2, 5], [13, 3], [13, 4], [13, 5], [3, 2], [12, 2]].forEach(([a, b]) => put(a, b, PAL.a));
    if (level >= 5) { [5, 7, 8, 10].forEach(cx => put(cx, 0, PAL.g)); for (let cx = 5; cx <= 10; cx++) put(cx, 1, PAL.g); }
  }

  // ---------- feedback ----------
  let toastT;
  function toast(msg) {
    const t = $('toast');
    t.textContent = msg; t.hidden = false;
    clearTimeout(toastT);
    toastT = setTimeout(() => { t.hidden = true; }, 2800);
  }
  function celebrate(b, a, label) {
    if (!b) return;
    if (a.hero.level > b.hero.level) { toast('Уровень ' + a.hero.level + '!' + (PERKS[a.hero.level] ? ' Персонаж получил ' + PERKS[a.hero.level] + '.' : '')); return; }
    const got = a.achievements.find(x => x.got && !(b.achievements.find(y => y.id === x.id) || {}).got);
    if (got) { toast('Достижение: ' + got.name); return; }
    if (a.hero.xp > b.hero.xp) toast('+' + (a.hero.xp - b.hero.xp) + ' XP' + (label ? ' · ' + label : ''));
  }
  async function copy(text) {
    try { await navigator.clipboard.writeText(text); }
    catch (e) {
      // http без TLS: Clipboard API недоступен, копируем по-старому
      const ta = document.createElement('textarea');
      ta.value = text; document.body.appendChild(ta); ta.select();
      document.execCommand('copy'); ta.remove();
    }
    toast('Скопировано — вставь в Claude Code');
  }

  // ---------- events ----------
  const skillName = id => (S.plan.skills.find(s => s.id === id) || {}).name || id;

  document.addEventListener('click', e => {
    const t = e.target;
    const tile = t.closest('.tile');
    if (tile) { sel = +tile.dataset.i; openGuide = null; render(); return; }
    const nav = t.closest('[data-nav]');
    if (nav) {
      const v = +nav.dataset.nav, first = S.calendar.findIndex(d => !d.pre);
      sel = v === 0 ? homeIdx() : Math.min(S.calendar.length - 1, Math.max(first, sel + v));
      openGuide = null; render(); return;
    }
    const act = t.closest('.act');
    if (act) {
      const day = S.calendar[sel], skill = act.dataset.skill, v = +act.dataset.v;
      const cur = (S.progress.marks[day.date] || {})[skill] || 0;
      mutate('PUT', '/api/marks/' + day.date + '/' + skill, { level: cur === v ? 0 : v }, skillName(skill));
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
    const ans = t.closest('[data-answer]');
    if (ans) {
      const id = ans.dataset.answer, v = (drafts['a:' + id] || '').trim();
      if (!v) { toast('Напиши хоть пару слов — или нажми «Не знаю»'); return; }
      mutate('PATCH', '/api/arena/' + id, { answer: v }).then(() => { delete drafts['a:' + id]; toast('Ответ сохранён — попроси разбор в Claude Code'); });
      return;
    }
    const dn = t.closest('[data-dunno]');
    if (dn) { mutate('PATCH', '/api/arena/' + dn.dataset.dunno, { answer: 'Не знаю' }).then(() => toast('Это нормально — разберём тему с нуля')); }
  });

  document.addEventListener('input', e => {
    const t = e.target;
    if (t.id === 'note') drafts['note:' + S.calendar[sel].date] = t.value;
    else if (t.dataset.aid) drafts['a:' + t.dataset.aid] = t.value;
    else if (t.dataset.rf) drafts['r:' + t.dataset.rw + ':' + t.dataset.rf] = t.value;
  });

  document.addEventListener('change', e => {
    const t = e.target;
    if (t.id === 'heroName') {
      const name = t.value.trim();
      if (name) mutate('PUT', '/api/hero', { name });
    } else if (t.id === 'note') {
      const date = S.calendar[sel].date;
      mutate('PATCH', '/api/days/' + date, { note: t.value }).then(() => { delete drafts['note:' + date]; });
    } else if (t.dataset.item) {
      mutate('PUT', '/api/weeks/' + t.dataset.week + '/items/' + t.dataset.item, { done: t.checked }, 'удар по боссу');
    } else if (t.dataset.rf) {
      const week = t.dataset.rw, cur = S.progress.reviews[week] || {}, body = {};
      REVIEW_FIELDS.forEach(([f]) => {
        const k = 'r:' + week + ':' + f;
        body[f] = drafts[k] != null ? drafts[k] : (cur[f] || '');
      });
      mutate('PUT', '/api/weeks/' + week + '/review', body, 'хроника').then(() => {
        REVIEW_FIELDS.forEach(([f]) => { delete drafts['r:' + week + ':' + f]; });
      });
    }
  });

  // Наставник пишет разборы через API — подтягиваем их, пока вкладка открыта.
  const typing = () => { const a = document.activeElement; return a && (a.tagName === 'TEXTAREA' || a.tagName === 'INPUT'); };
  setInterval(() => { if (S && document.visibilityState === 'visible' && !typing()) load(); }, 30000);
  document.addEventListener('visibilitychange', () => { if (S && document.visibilityState === 'visible' && !typing()) load(); });

  load();
})();
