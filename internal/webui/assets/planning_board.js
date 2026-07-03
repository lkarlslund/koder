(function () {
  function asText(value) {
    return String(value || '').trim();
  }

  function itemKey(item) {
    return asText(item && (item.key || item.Key));
  }

  function statusValue(item, fallback) {
    return asText(item && (item.status || item.Status)) || fallback;
  }

  function positionValue(item) {
    const raw = item && (item.position ?? item.Position);
    const value = Number(raw);
    return Number.isFinite(value) ? value : 0;
  }

  function sortByPositionAndKey(items) {
    return [...(items || [])].sort((a, b) => {
      const byPosition = positionValue(a) - positionValue(b);
      if (byPosition) return byPosition;
      return itemKey(a).localeCompare(itemKey(b));
    });
  }

  window.koderBoardApp = function () {
    return {
      sessionID: '',
      parentChatID: '',
      projectRoot: '',
      loading: true,
      saving: false,
      error: '',
      plan: {summary: '', milestones: []},
      tasksByMilestone: {},
      pollTimer: 0,
      dragTaskKey: '',
      dragMilestoneKey: '',
      milestoneEditor: {open: false, key: '', title: '', status: 'pending', depends_on_key: '', notes: ''},
      taskEditor: {open: false, key: '', milestone_key: '', status: 'pending', content: '', note: ''},
      milestoneStatuses: [
        {key: 'pending', label: 'Pending', badge: 'planning-badge-muted'},
        {key: 'decomposing', label: 'Decomposing', badge: 'planning-badge-info'},
        {key: 'ready', label: 'Ready', badge: 'planning-badge-ready'},
        {key: 'executing', label: 'Executing', badge: 'planning-badge-active'},
        {key: 'completed', label: 'Completed', badge: 'planning-badge-done'},
        {key: 'blocked', label: 'Blocked', badge: 'planning-badge-blocked'},
        {key: 'cancelled', label: 'Cancelled', badge: 'planning-badge-cancelled'}
      ],
      taskStatuses: [
        {key: 'pending', label: 'Pending', icon: 'bi-circle'},
        {key: 'in_progress', label: 'In progress', icon: 'bi-arrow-repeat'},
        {key: 'completed', label: 'Completed', icon: 'bi-check-circle'},
        {key: 'cancelled', label: 'Cancelled', icon: 'bi-x-circle'}
      ],

      init() {
        const match = location.pathname.match(/^\/s\/([^/]+)\/board$/);
        this.sessionID = match ? decodeURIComponent(match[1]) : '';
        this.parentChatID = String(new URLSearchParams(location.search).get('chat') || '').trim();
        this.loadBoard();
        this.pollTimer = window.setInterval(() => {
          if (!document.hidden && !this.saving) this.loadBoard({quiet: true});
        }, 5000);
        document.addEventListener('visibilitychange', () => {
          if (!document.hidden) this.loadBoard({quiet: true});
        });
      },

      sessionURL() {
        if (!this.sessionID) return '/';
        const base = '/s/' + encodeURIComponent(this.sessionID);
        return this.parentChatID ? base + '/c/' + encodeURIComponent(this.parentChatID) : base;
      },

      boardAPI(path) {
        return '/api/sessions/' + encodeURIComponent(this.sessionID) + '/board' + (path || '');
      },

      async loadBoard(options) {
        if (!this.sessionID) {
          this.error = 'Session id is missing';
          this.loading = false;
          return;
        }
        const quiet = !!(options && options.quiet);
        if (!quiet) this.loading = true;
        try {
          const resp = await fetch(this.boardAPI(''), {cache: 'no-store'});
          if (!resp.ok) throw new Error(await resp.text());
          this.applyBoard(await resp.json());
          this.error = '';
        } catch (err) {
          this.error = asText(err && err.message) || 'Failed to load board';
        } finally {
          this.loading = false;
        }
      },

      applyBoard(data) {
        this.projectRoot = asText(data.project_root || data.ProjectRoot);
        this.plan = data.plan || data.Plan || {summary: '', milestones: []};
        this.tasksByMilestone = data.tasks_by_milestone || data.TasksByKey || {};
      },

      async postBoard(path, body) {
        this.saving = true;
        try {
          const resp = await fetch(this.boardAPI(path), {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(body || {})
          });
          if (!resp.ok) throw new Error(await resp.text());
          this.applyBoard(await resp.json());
          this.error = '';
        } catch (err) {
          this.error = asText(err && err.message) || 'Save failed';
          throw err;
        } finally {
          this.saving = false;
        }
      },

      async startChatForMilestone(milestone, task) {
        if (!this.parentChatID) {
          this.error = 'Open this board from a chat to start worker chats.';
          return;
        }
        const milestoneKey = this.milestoneKey(milestone);
        const taskKey = task ? this.taskKey(task) : '';
        const title = taskKey ? milestoneKey + ' ' + taskKey : milestoneKey + ' worker';
        this.saving = true;
        try {
          const resp = await fetch(this.boardAPI('/chats/start'), {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({
              parent_chat_id: this.parentChatID,
              milestone_key: milestoneKey,
              task_key: taskKey,
              title
            })
          });
          if (!resp.ok) throw new Error(await resp.text());
          this.error = '';
          await this.loadBoard({quiet: true});
        } catch (err) {
          this.error = asText(err && err.message) || 'Start chat failed';
        } finally {
          this.saving = false;
        }
      },

      milestones() {
        return sortByPositionAndKey(this.plan.milestones || this.plan.Milestones || []);
      },

      milestoneKey(milestone) {
        return itemKey(milestone);
      },

      milestoneTitle(milestone) {
        return asText(milestone && (milestone.title || milestone.Title)) || this.milestoneKey(milestone);
      },

      milestoneNotes(milestone) {
        return asText(milestone && (milestone.notes || milestone.Notes));
      },

      milestoneDependsOnKey(milestone) {
        return asText(milestone && (milestone.depends_on_key || milestone.DependsOnKey));
      },

      milestoneStatus(milestone) {
        return statusValue(milestone, 'pending');
      },

      milestoneStatusLabel(status) {
        const found = this.milestoneStatuses.find(item => item.key === status);
        return found ? found.label : status;
      },

      milestoneBadge(status) {
        const found = this.milestoneStatuses.find(item => item.key === status);
        return found ? found.badge : 'planning-badge-muted';
      },

      tasksForMilestone(milestone) {
        return sortByPositionAndKey(this.tasksByMilestone[this.milestoneKey(milestone)] || []);
      },

      tasksForStatus(milestone, status) {
        return this.tasksForMilestone(milestone).filter(task => this.taskStatus(task) === status);
      },

      taskKey(task) {
        return itemKey(task);
      },

      taskStatus(task) {
        return statusValue(task, 'pending');
      },

      taskContent(task) {
        return asText(task && (task.content || task.Content));
      },

      taskNote(task) {
        return asText(task && (task.note || task.Note));
      },

      milestoneTaskSummary(milestone) {
        const tasks = this.tasksForMilestone(milestone);
        const completed = tasks.filter(task => this.taskStatus(task) === 'completed').length;
        const active = tasks.filter(task => this.taskStatus(task) === 'in_progress').length;
        return completed + '/' + tasks.length + ' done' + (active ? ', ' + active + ' active' : '');
      },

      boardSummary() {
        const milestones = this.milestones();
        const taskCount = milestones.reduce((count, milestone) => count + this.tasksForMilestone(milestone).length, 0);
        return milestones.length + ' milestones · ' + taskCount + ' tasks';
      },

      openMilestoneEditor(milestone) {
        this.milestoneEditor = {
          open: true,
          key: milestone ? this.milestoneKey(milestone) : '',
          title: milestone ? this.milestoneTitle(milestone) : '',
          status: milestone ? this.milestoneStatus(milestone) : 'pending',
          depends_on_key: milestone ? this.milestoneDependsOnKey(milestone) : '',
          notes: milestone ? this.milestoneNotes(milestone) : ''
        };
      },

      closeMilestoneEditor() {
        this.milestoneEditor.open = false;
      },

      async saveMilestone() {
        if (!asText(this.milestoneEditor.title)) {
          this.error = 'Milestone title is required';
          return;
        }
        await this.postBoard('/milestones', {
          key: this.milestoneEditor.key,
          title: this.milestoneEditor.title,
          status: this.milestoneEditor.status,
          depends_on_key: this.milestoneEditor.depends_on_key,
          notes: this.milestoneEditor.notes
        });
        this.closeMilestoneEditor();
      },

      openTaskEditor(milestone, task, status) {
        const milestoneKey = milestone ? this.milestoneKey(milestone) : '';
        this.taskEditor = {
          open: true,
          key: task ? this.taskKey(task) : '',
          milestone_key: task ? asText(task.milestone_key || task.MilestoneKey) : milestoneKey,
          status: task ? this.taskStatus(task) : (status || 'pending'),
          content: task ? this.taskContent(task) : '',
          note: task ? this.taskNote(task) : ''
        };
      },

      closeTaskEditor() {
        this.taskEditor.open = false;
      },

      async saveTask() {
        if (!asText(this.taskEditor.content)) {
          this.error = 'Task text is required';
          return;
        }
        if (this.taskEditor.key) {
          await this.postBoard('/tasks/update', {
            task_key: this.taskEditor.key,
            milestone_key: this.taskEditor.milestone_key,
            status: this.taskEditor.status,
            content: this.taskEditor.content,
            note: this.taskEditor.note
          });
        } else {
          await this.postBoard('/tasks', {
            milestone_key: this.taskEditor.milestone_key,
            content: this.taskEditor.content
          });
        }
        this.closeTaskEditor();
      },

      startTaskDrag(task) {
        this.dragTaskKey = this.taskKey(task);
      },

      async dropTask(milestone, status) {
        const taskKey = this.dragTaskKey;
        this.dragTaskKey = '';
        if (!taskKey) return;
        const milestoneKey = this.milestoneKey(milestone);
        const position = this.tasksForMilestone(milestone).length;
        await this.postBoard('/tasks/update', {
          task_key: taskKey,
          milestone_key: milestoneKey,
          status,
          position
        });
      },

      startMilestoneDrag(milestone) {
        this.dragMilestoneKey = this.milestoneKey(milestone);
      },

      async dropMilestone(index) {
        const key = this.dragMilestoneKey;
        this.dragMilestoneKey = '';
        if (!key) return;
        const keys = this.milestones().map(item => this.milestoneKey(item)).filter(item => item !== key);
        keys.splice(index, 0, key);
        await this.postBoard('/milestones/order', {keys});
      },

      async moveMilestone(index, delta) {
        const keys = this.milestones().map(item => this.milestoneKey(item));
        const next = index + delta;
        if (next < 0 || next >= keys.length) return;
        const [key] = keys.splice(index, 1);
        keys.splice(next, 0, key);
        await this.postBoard('/milestones/order', {keys});
      }
    };
  };
})();
