# Changelog

## [1.2.0](https://github.com/usestring/gate-inbox/compare/v1.1.0...v1.2.0) (2026-11-14)


### Features

* add a "fix it" default snippet ([#541](https://github.com/usestring/gate-inbox/issues/541)) ([2e005f9](https://github.com/usestring/gate-inbox/commit/2e005f9093cf843c29588356389703e223bc49a3))
* add global pane navigation in list order ([#523](https://github.com/usestring/gate-inbox/issues/523)) ([2fea20e](https://github.com/usestring/gate-inbox/commit/2fea20ece167f8c1a6b5a94e8fa4557bacc6f3f5))
* **extension:** let a board extension migrate a session to another account ([#511](https://github.com/usestring/gate-inbox/issues/511)) ([edaa6b9](https://github.com/usestring/gate-inbox/commit/edaa6b9fd42ce4cf2c4552c0405a5a66635990b6))
* **extension:** let extensions record placed spans, and keep private values off every span ([#577](https://github.com/usestring/gate-inbox/issues/577)) ([9197f33](https://github.com/usestring/gate-inbox/commit/9197f3339ff23ec4b8a531804cd52775194ab6e9))
* **extension:** tell board extensions where the operator is ([#612](https://github.com/usestring/gate-inbox/issues/612)) ([a31415a](https://github.com/usestring/gate-inbox/commit/a31415a99adaaf8aa459486dc82d9f38c7d0326a))
* **extension:** tell board extensions which actions the operator runs ([#625](https://github.com/usestring/gate-inbox/issues/625)) ([be047d0](https://github.com/usestring/gate-inbox/commit/be047d0be1cf2fc6d6a4a6f8fd9924f70e41db05))
* **hibernate:** show hibernated sessions as hibernating and wake them on focus ([#589](https://github.com/usestring/gate-inbox/issues/589)) ([c50327f](https://github.com/usestring/gate-inbox/commit/c50327f58494a5d4b13a6fb276f59065109882a1))
* **inbox:** start routers as inbox-&lt;source&gt; and have them name themselves ([#510](https://github.com/usestring/gate-inbox/issues/510)) ([6b22f42](https://github.com/usestring/gate-inbox/commit/6b22f427141d19d2239a6570bfda79c1995501ce))
* **keys:** layer per-device key overrides over keys.toml ([#587](https://github.com/usestring/gate-inbox/issues/587)) ([c892dd3](https://github.com/usestring/gate-inbox/commit/c892dd37e2bace1cc0a1fdd7c5409bc739282547))
* **keys:** wake every hibernated session with ctrl+alt+w ([#640](https://github.com/usestring/gate-inbox/issues/640)) ([d1faaee](https://github.com/usestring/gate-inbox/commit/d1faaee8d9e847219b46fc085e3e971672e3a0e1))
* **launch:** add a confirm launches setting so agents can delegate without asking ([#574](https://github.com/usestring/gate-inbox/issues/574)) ([8b48886](https://github.com/usestring/gate-inbox/commit/8b48886de9f685fb03725442e0125cce410336e7))
* list published artifacts in a session's work rows through an extension work API ([#537](https://github.com/usestring/gate-inbox/issues/537)) ([da8ee7b](https://github.com/usestring/gate-inbox/commit/da8ee7b176fe82430ad96f88a03f5fedb0e69de7))
* **mcpserver:** resolve each tool call's caller per request ([#568](https://github.com/usestring/gate-inbox/issues/568)) ([8a57828](https://github.com/usestring/gate-inbox/commit/8a57828a619fa14209f0cbbfb437a3139b51b67a))
* **mcpserver:** serve every claude session from one shared MCP server ([#570](https://github.com/usestring/gate-inbox/issues/570)) ([171c7ea](https://github.com/usestring/gate-inbox/commit/171c7eaffd8ab2d5f11f016851dba33fec6713d5))
* open session search with Ctrl+F ([#524](https://github.com/usestring/gate-inbox/issues/524)) ([f670787](https://github.com/usestring/gate-inbox/commit/f6707877a507e4fca2c96b19e29d05edadf7937c))
* **opencode:** attach launched sessions to one shared server per board ([#569](https://github.com/usestring/gate-inbox/issues/569)) ([54cf912](https://github.com/usestring/gate-inbox/commit/54cf912154717c578125203bea6aca21e12851f2))
* **park:** bring every session back on unpark, work in flight first ([#557](https://github.com/usestring/gate-inbox/issues/557)) ([cf47be8](https://github.com/usestring/gate-inbox/commit/cf47be897333655898b0649e8c50a92535257edd))
* pick held dialog choices with a-c in the hotkey menu ([#542](https://github.com/usestring/gate-inbox/issues/542)) ([2f6ca8d](https://github.com/usestring/gate-inbox/commit/2f6ca8dba32ccc100060ea37a0472dede7752497))
* **relaunch:** let moves off a spent account go ahead of routine relaunches ([#547](https://github.com/usestring/gate-inbox/issues/547)) ([376e3dd](https://github.com/usestring/gate-inbox/commit/376e3dd9b9ec4ff001fd98fd23481980318ee27a))
* return PR CI failures to the owning session [stack 1/2] ([#583](https://github.com/usestring/gate-inbox/issues/583)) ([cabc3b2](https://github.com/usestring/gate-inbox/commit/cabc3b29caad6cd38a934c45fae74639c7db04de))
* **reviews:** carry session priority into PR review ([#617](https://github.com/usestring/gate-inbox/issues/617)) ([f60eb41](https://github.com/usestring/gate-inbox/commit/f60eb41394f74aa8ad38949f05d2441d9a08f2d2))
* **reviews:** open a pull request's page from the review queue ([#439](https://github.com/usestring/gate-inbox/issues/439)) ([b0d3a0a](https://github.com/usestring/gate-inbox/commit/b0d3a0a1b49f571ae297af797e136bc09019603c))
* send native PR attention to the owning session [stack 2/2] ([#585](https://github.com/usestring/gate-inbox/issues/585)) ([c375769](https://github.com/usestring/gate-inbox/commit/c375769ad0a7437755855fb348b4b4d49b24e915))
* **sessioncmd:** hibernate idle agents to give their memory back without losing their work ([#553](https://github.com/usestring/gate-inbox/issues/553)) ([ae81018](https://github.com/usestring/gate-inbox/commit/ae81018bad385a5a00cd10aac0622d1845f359b2))
* **snippets:** answer a question's free-text row with a snippet ([#517](https://github.com/usestring/gate-inbox/issues/517)) ([96e77ef](https://github.com/usestring/gate-inbox/commit/96e77ef2b431139e483395f663b2c2caf3120110))
* support both JEV and Clef classifiers ([#616](https://github.com/usestring/gate-inbox/issues/616)) ([43cc456](https://github.com/usestring/gate-inbox/commit/43cc4568bec854c40b63071c0a7c482ae5dff5f8))
* **task:** assign tasks to one session and hand them to a child at spawn [stack 2/2] ([#531](https://github.com/usestring/gate-inbox/issues/531)) ([ab3b476](https://github.com/usestring/gate-inbox/commit/ab3b47623cfcac0f2a521e21a47945bdb756b7c4))
* **task:** record task creators and scope a bare claim to the caller's fleet [stack 1/2] ([#528](https://github.com/usestring/gate-inbox/issues/528)) ([1b6cdd0](https://github.com/usestring/gate-inbox/commit/1b6cdd06f95066e5d3da85be145d51c5f89fec87))
* **tmux:** name a slow escape-time in the startup tmux check ([#546](https://github.com/usestring/gate-inbox/issues/546)) ([9810370](https://github.com/usestring/gate-inbox/commit/98103700605e572db13b84360de16ba961582c72))
* **triage:** add triage order modes and a keep-alive toggle to Settings [stack 2/2] ([#594](https://github.com/usestring/gate-inbox/issues/594)) ([2f20ee3](https://github.com/usestring/gate-inbox/commit/2f20ee344719e2b56ff8b0e58a8a0f2720bd9b11))
* **ui:** add a row detail setting that leaves state to the marks ([#631](https://github.com/usestring/gate-inbox/issues/631)) ([16693c6](https://github.com/usestring/gate-inbox/commit/16693c6d0ce59e965983152c131ff3275cf5650c))
* **ui:** add a setting to turn off the idle switch to sessions needing attention ([#573](https://github.com/usestring/gate-inbox/issues/573)) ([6dc8639](https://github.com/usestring/gate-inbox/commit/6dc8639704192985bd40783dfef7c8f0caa6a2e8))
* **ui:** choose sessions, reviews, or both in triage ([#610](https://github.com/usestring/gate-inbox/issues/610)) ([df51ba2](https://github.com/usestring/gate-inbox/commit/df51ba24b7fa58725651f6467e654724e7738829))
* **ui:** journal inputs and dump a stuck board on demand ([#516](https://github.com/usestring/gate-inbox/issues/516)) ([8959091](https://github.com/usestring/gate-inbox/commit/89590911b7820184c4a1f40cabea605f8b73e878))
* **ui:** make compressed focus a default-off F3 toggle ([#509](https://github.com/usestring/gate-inbox/issues/509)) ([eaa1dd1](https://github.com/usestring/gate-inbox/commit/eaa1dd13716210d8a050393f068b9aabdc1e57dc))
* **ui:** notify when the idle handover switches focus ([#518](https://github.com/usestring/gate-inbox/issues/518)) ([f533ab2](https://github.com/usestring/gate-inbox/commit/f533ab23c1bfbbfdbd2025c2be6c47845ec07e4c))
* **ui:** run the next command JEV finds in a turn with a key ([#626](https://github.com/usestring/gate-inbox/issues/626)) ([f689b75](https://github.com/usestring/gate-inbox/commit/f689b75da607382b65bb600fe789c19de144e3e0))
* **ui:** select sidebar sessions with mouse clicks ([#525](https://github.com/usestring/gate-inbox/issues/525)) ([b24ba5c](https://github.com/usestring/gate-inbox/commit/b24ba5c8a2160c47e509de5d61896eb30b25cff3))
* **ui:** show the current session ID in the session summary ([#560](https://github.com/usestring/gate-inbox/issues/560)) ([5a4e18a](https://github.com/usestring/gate-inbox/commit/5a4e18a454bb7c3c7e8d2ea6ffa364312e542b7c))
* **ui:** show total sessions in the sidebar summary ([#615](https://github.com/usestring/gate-inbox/issues/615)) ([325c8cc](https://github.com/usestring/gate-inbox/commit/325c8cc5c0419571fd836cf53a2cd772b12539f5))
* **ui:** step through accounts on one row of the switch-account card ([#544](https://github.com/usestring/gate-inbox/issues/544)) ([49ab5a5](https://github.com/usestring/gate-inbox/commit/49ab5a505d224365854b12610bc8a314da8fb338))
* **ui:** stream live output in compressed conversations ([#507](https://github.com/usestring/gate-inbox/issues/507)) ([91d8363](https://github.com/usestring/gate-inbox/commit/91d8363869f73c3486171ddff4dace985f660dd2))
* **ui:** support conditional and positional list consistency ([#527](https://github.com/usestring/gate-inbox/issues/527)) ([b4ee310](https://github.com/usestring/gate-inbox/commit/b4ee310e4529ff0d97c842371d527fd244887b4c))


### Bug Fixes

* **adopt:** identify an interpreted agent whose argv[0] is not its executable ([#633](https://github.com/usestring/gate-inbox/issues/633)) ([71e534d](https://github.com/usestring/gate-inbox/commit/71e534dbfff92f3618b8b62fc018ad054bce9d5c))
* **adopt:** recognise a native agent binary that tmux names with .exe ([#641](https://github.com/usestring/gate-inbox/issues/641)) ([74e362c](https://github.com/usestring/gate-inbox/commit/74e362c724676f3f767528e6bb494879b23fa7af))
* **app:** send the board's stderr to a log file so system warnings can't draw over it ([#642](https://github.com/usestring/gate-inbox/issues/642)) ([7c96a60](https://github.com/usestring/gate-inbox/commit/7c96a606e3c1640cc3d0fc0ce6a14fd14370c665))
* **capture:** keep local paths out of capture tapes and recordings ([#602](https://github.com/usestring/gate-inbox/issues/602)) ([51743d0](https://github.com/usestring/gate-inbox/commit/51743d047a48dbbfa22356dd7c2aa550d3b621a3))
* **capture:** stop the board tmux server when a run fails ([#539](https://github.com/usestring/gate-inbox/issues/539)) ([23b9fd0](https://github.com/usestring/gate-inbox/commit/23b9fd014d0344b530a867056f2d8253786a9198))
* **claude:** show when sign-in is required ([#562](https://github.com/usestring/gate-inbox/issues/562)) ([ab88a6e](https://github.com/usestring/gate-inbox/commit/ab88a6eb061664122f87260f43e1977a6d6d4ad9))
* handle stale managed identity in shared Codex stops ([#487](https://github.com/usestring/gate-inbox/issues/487)) ([be16d1f](https://github.com/usestring/gate-inbox/commit/be16d1fc94f5c26db76c314cd494e7ca50e314a1))
* honor existing authorization for session delegation ([#555](https://github.com/usestring/gate-inbox/issues/555)) ([bb6df65](https://github.com/usestring/gate-inbox/commit/bb6df6538089d5f075d7a0f6dee91dce88ac2d38))
* **hooks:** unregister global hooks when the settings path runs through a symlink ([#632](https://github.com/usestring/gate-inbox/issues/632)) ([4df1b68](https://github.com/usestring/gate-inbox/commit/4df1b68672f6dbc13e9e166f1c3206abd47cacce))
* **inbox:** log why a POST /inbox was rejected ([#618](https://github.com/usestring/gate-inbox/issues/618)) ([c7bcc26](https://github.com/usestring/gate-inbox/commit/c7bcc268ee6a1456020e584ed88eb6e084934929))
* **opencode:** detect and start the shared server reliably on v2 ([#644](https://github.com/usestring/gate-inbox/issues/644)) ([43a20dc](https://github.com/usestring/gate-inbox/commit/43a20dc6b6874607ce4d7b43424f6196aae543a1))
* refuse queued relaunches during macOS memory pressure ([#543](https://github.com/usestring/gate-inbox/issues/543)) ([38fa41b](https://github.com/usestring/gate-inbox/commit/38fa41b44930124e3089bccefab92e89669874b0))
* reject duplicate captured conversation claims [split 2/2] ([#580](https://github.com/usestring/gate-inbox/issues/580)) ([feaebef](https://github.com/usestring/gate-inbox/commit/feaebef504a6884531cea01bdf7578c545c5f14b))
* require evidence before binding Codex conversations [split 1/2] ([#579](https://github.com/usestring/gate-inbox/issues/579)) ([711a95b](https://github.com/usestring/gate-inbox/commit/711a95b407e10d18187edd4f4aad91c9cf20e7e7))
* **reservations:** resolve paths against the session directory ([#559](https://github.com/usestring/gate-inbox/issues/559)) ([4dd807f](https://github.com/usestring/gate-inbox/commit/4dd807fcc49bd1f8cdd5550496cd06cf6f72e3af))
* resolve shutdown caller behind stale thread hints ([#576](https://github.com/usestring/gate-inbox/issues/576)) ([cd1b939](https://github.com/usestring/gate-inbox/commit/cd1b939b70af2b0413690ff029fc0272f82badd1))
* **reviews:** flag stale PR briefings ([#561](https://github.com/usestring/gate-inbox/issues/561)) ([5be4557](https://github.com/usestring/gate-inbox/commit/5be45571e33f36ed7d86722ef50d454235de519f))
* **reviews:** preserve the global Ctrl+C interrupt ([#519](https://github.com/usestring/gate-inbox/issues/519)) ([f13f577](https://github.com/usestring/gate-inbox/commit/f13f577bf7e651de3861aa90f3fa94006514b84d))
* **sandbox:** stop restarting sessions over a raced .git/config.lock ([#515](https://github.com/usestring/gate-inbox/issues/515)) ([53cfea1](https://github.com/usestring/gate-inbox/commit/53cfea10147624a61018ff1b87a45d6e733e2f8e))
* **status:** distinguish background shells from active turns ([#614](https://github.com/usestring/gate-inbox/issues/614)) ([bcd7751](https://github.com/usestring/gate-inbox/commit/bcd775157f9c757b785ec8dc1a97235263b4c4df))
* **status:** read only the banner's rows when Claude's limit menu is up ([#545](https://github.com/usestring/gate-inbox/issues/545)) ([d9737da](https://github.com/usestring/gate-inbox/commit/d9737dae3e0875676f4996ed9d3f7c04c59579d4))
* **stop:** resolve shared OpenCode callers by pane marker ([#526](https://github.com/usestring/gate-inbox/issues/526)) ([14f9574](https://github.com/usestring/gate-inbox/commit/14f9574f26c56559aea8a23282d5e7046e0446f4))
* **store:** compare file reservations by their canonical path ([#635](https://github.com/usestring/gate-inbox/issues/635)) ([1b1d529](https://github.com/usestring/gate-inbox/commit/1b1d529a68b5d408dc3dbb6a83f7862513a75459))
* **store:** leave parked children out of the exited-child sweep ([#558](https://github.com/usestring/gate-inbox/issues/558)) ([1511462](https://github.com/usestring/gate-inbox/commit/1511462b06ce39594b646fa9be93fceb358338ab))
* **tasks:** give task claims back when a session is archived or killed ([#529](https://github.com/usestring/gate-inbox/issues/529)) ([f6dfbc8](https://github.com/usestring/gate-inbox/commit/f6dfbc8f6d39d455e1810c43a539905fb90d3d1a))
* **tmux:** exec the launch shell so an exited agent stops reading alive on Linux ([#650](https://github.com/usestring/gate-inbox/issues/650)) ([296e853](https://github.com/usestring/gate-inbox/commit/296e853e8acc5f56c3a20f08d696c174d1d66d50))
* **tmux:** never start a tmux server from the root-key probe or binding setup ([#548](https://github.com/usestring/gate-inbox/issues/548)) ([7f01ffe](https://github.com/usestring/gate-inbox/commit/7f01ffe185ebf795a5eb97164fbc02b30dd13f49))
* **tmux:** reap processes on every teardown path, including ones that escaped the window ([#551](https://github.com/usestring/gate-inbox/issues/551)) ([c116109](https://github.com/usestring/gate-inbox/commit/c116109a5b6f5d8deb94ebff41e958d20906fa7a))
* **triage:** hand over children whose parent is gone ([#581](https://github.com/usestring/gate-inbox/issues/581)) ([73d0600](https://github.com/usestring/gate-inbox/commit/73d0600e2fc9f106c73999879a5c60718f14b3b5))
* **triage:** hand over hibernated sessions after everything else ([#639](https://github.com/usestring/gate-inbox/issues/639)) ([01d18bf](https://github.com/usestring/gate-inbox/commit/01d18bfb462179075e3263518c088c4b58352993))
* **triage:** hand over kept-warm idle sessions by default ([#649](https://github.com/usestring/gate-inbox/issues/649)) ([f341da7](https://github.com/usestring/gate-inbox/commit/f341da711f1f58d369ebf69dc5c204d29502b4e7))
* **triage:** keep an answered session off the walk until the board sees its new turn ([#629](https://github.com/usestring/gate-inbox/issues/629)) ([c66e7c2](https://github.com/usestring/gate-inbox/commit/c66e7c2093001f4c76e6bb5d6f2ef2bec0673948))
* **triage:** keep working sessions below ready sessions ([#554](https://github.com/usestring/gate-inbox/issues/554)) ([4f7abda](https://github.com/usestring/gate-inbox/commit/4f7abda57fcaeb3c696bd5d67ccd7db5abc7a248))
* **triage:** rotate through questions instead of cycling warm sessions [stack 1/2] ([#590](https://github.com/usestring/gate-inbox/issues/590)) ([032c6d5](https://github.com/usestring/gate-inbox/commit/032c6d57eb399d24ced008b33868ef9c55974972))
* **triage:** sort background sessions above working ones whatever their tier ([#627](https://github.com/usestring/gate-inbox/issues/627)) ([a51a8d9](https://github.com/usestring/gate-inbox/commit/a51a8d995d2456d0cc6eafed84baef4b6cfb1bb5))
* **ui:** advance triage after muting a session ([#512](https://github.com/usestring/gate-inbox/issues/512)) ([0a290a2](https://github.com/usestring/gate-inbox/commit/0a290a2fd9f0acfbdd95831d68a0a6cb48f7db6d))
* **ui:** defer errored sessions behind actionable work ([#522](https://github.com/usestring/gate-inbox/issues/522)) ([74b914c](https://github.com/usestring/gate-inbox/commit/74b914ccf6747b4eae547e5644ed37e5fc06902e))
* **ui:** ignore delayed polls that requeue working sessions ([#552](https://github.com/usestring/gate-inbox/issues/552)) ([967d033](https://github.com/usestring/gate-inbox/commit/967d0333b0f5cc997912772a6db96d6a776f82f8))
* **ui:** keep model commands in the current triage session ([#535](https://github.com/usestring/gate-inbox/issues/535)) ([eb4ed2c](https://github.com/usestring/gate-inbox/commit/eb4ed2c94ebf4da70e90ef8a7f684a91ee8cc806))
* **ui:** keep tab selection under operator control ([#588](https://github.com/usestring/gate-inbox/issues/588)) ([c3440fc](https://github.com/usestring/gate-inbox/commit/c3440fc653b894a18fc8baa971c7967bf1273801))
* **ui:** pick held dialog choices with a through e ([#578](https://github.com/usestring/gate-inbox/issues/578)) ([c1b23da](https://github.com/usestring/gate-inbox/commit/c1b23da8ddc34721c3a013fee95f898e516ac7b9))
* **ui:** preserve Codex failed turns ([#611](https://github.com/usestring/gate-inbox/issues/611)) ([b14d595](https://github.com/usestring/gate-inbox/commit/b14d5957ff1880280c237a6d3e62a7a487290135))
* **ui:** preserve negative priority for parents in triage ([#572](https://github.com/usestring/gate-inbox/issues/572)) ([719f92b](https://github.com/usestring/gate-inbox/commit/719f92beff686359441f5ba85cee3f391175e3e3))
* **ui:** preview single Claude questions ([#564](https://github.com/usestring/gate-inbox/issues/564)) ([52dc5bf](https://github.com/usestring/gate-inbox/commit/52dc5bf3c1a42c97cd375c9d297729c47c39cbdc))
* **ui:** refresh adopted targets after external relaunch ([#613](https://github.com/usestring/gate-inbox/issues/613)) ([9d77325](https://github.com/usestring/gate-inbox/commit/9d7732501d1029adf2effa95b5629388c4a74130))
* **ui:** relay a child's background rest to its parent ([#575](https://github.com/usestring/gate-inbox/issues/575)) ([812c41b](https://github.com/usestring/gate-inbox/commit/812c41b4e404f283c1ed952b575c880863d71f42))
* **ui:** show the selected session's context while focused, drop the dock's session count ([#647](https://github.com/usestring/gate-inbox/issues/647)) ([fbae9f5](https://github.com/usestring/gate-inbox/commit/fbae9f533fa6fa4e1428b0ffb5ed7ec5db0efc9d))
* **ui:** stop re-offering a stale limit card after an account move ([#514](https://github.com/usestring/gate-inbox/issues/514)) ([f77b0c0](https://github.com/usestring/gate-inbox/commit/f77b0c08e0e987695553d71511be7ddc01a3a337))
* **ui:** stop the limit card defaulting to Codex; make other CLIs opt-in ([#538](https://github.com/usestring/gate-inbox/issues/538)) ([f8986d6](https://github.com/usestring/gate-inbox/commit/f8986d69092ce5580722a1003fb9856403daf12d))


### Performance Improvements

* **app:** build the external fixture module once per test binary ([#636](https://github.com/usestring/gate-inbox/issues/636)) ([1fd456c](https://github.com/usestring/gate-inbox/commit/1fd456c1679064aff9b2ff5acefb33d5830d4fd4))
* **convo:** remember PendingAskCall's answer until the transcript changes ([#550](https://github.com/usestring/gate-inbox/issues/550)) ([6e3d448](https://github.com/usestring/gate-inbox/commit/6e3d448b0bad0befc0b6110349a3079bbea7f5c5))
* **mcpserver:** bound each session's MCP server to what one session needs ([#565](https://github.com/usestring/gate-inbox/issues/565)) ([4b65735](https://github.com/usestring/gate-inbox/commit/4b65735df48099b53fba2ca76bd79ebe5d78739d))
* **opencode:** trim the memory of each launched opencode session ([#566](https://github.com/usestring/gate-inbox/issues/566)) ([421a75b](https://github.com/usestring/gate-inbox/commit/421a75bf12dfe3b45309e6a516b2058839025f11))
* reuse Go test caches in optional local checks ([#628](https://github.com/usestring/gate-inbox/issues/628)) ([29e89c1](https://github.com/usestring/gate-inbox/commit/29e89c1204a414a778e742f1751108d4c8182b6f))
* **sessioncmd:** serve account, transcript and inbox board reads from pooled store handles ([#549](https://github.com/usestring/gate-inbox/issues/549)) ([d3e2b83](https://github.com/usestring/gate-inbox/commit/d3e2b831acceadb77824b113ca7be0442a767a7e))
* **ui:** skip quiet resting panes and batch work-tracker captures ([#513](https://github.com/usestring/gate-inbox/issues/513)) ([abec890](https://github.com/usestring/gate-inbox/commit/abec8906e5d0aba1ca1cc8ece4ab227c41a3e91d))

## [1.1.0](https://github.com/usestring/gate-inbox/compare/v1.0.0...v1.1.0) (2026-11-02)


### Features

* add skip-permissions toggles for a session and its children ([#429](https://github.com/usestring/gate-inbox/issues/429)) ([211013b](https://github.com/usestring/gate-inbox/commit/211013b66733151003108b4a8ec38352bcdcd2d3))
* **adopt:** keep outside panes as-is by default and offer to bring them in [stack 13/15] ([#277](https://github.com/usestring/gate-inbox/issues/277)) ([d9ff666](https://github.com/usestring/gate-inbox/commit/d9ff6660aaa9f84aee643c5d455dd08da7c5d6db))
* **adopt:** take a Claude Code pane the moment it announces itself [stack 12/15] ([#276](https://github.com/usestring/gate-inbox/issues/276)) ([0826a9d](https://github.com/usestring/gate-inbox/commit/0826a9dff4d5e455b8c914b2a309d6e0c2ddf176))
* **cli:** add accounts and switch-account commands ([#472](https://github.com/usestring/gate-inbox/issues/472)) ([0595ebb](https://github.com/usestring/gate-inbox/commit/0595ebba9af056b68b2ff3d434c2302ba4d5c396))
* **cli:** let gate-inbox spawn --nest=false launch your own session from a plain shell ([#466](https://github.com/usestring/gate-inbox/issues/466)) ([cb30d10](https://github.com/usestring/gate-inbox/commit/cb30d10d20289b842c33d53d7ef2a57fcbbe1d35))
* delay compact-window downgrades and show the window in the rail ([#433](https://github.com/usestring/gate-inbox/issues/433)) ([d0c0ca5](https://github.com/usestring/gate-inbox/commit/d0c0ca5b4245232be415ef06e8d821fdd738bc5c))
* **extension:** give each pass row the session's effective priority tier ([#417](https://github.com/usestring/gate-inbox/issues/417)) ([b654036](https://github.com/usestring/gate-inbox/commit/b654036dc8db5e8feb934af4445cee4743ecb428))
* **extension:** let a session spend its user's chosen answer to an Approval question once ([#424](https://github.com/usestring/gate-inbox/issues/424)) ([2b61da3](https://github.com/usestring/gate-inbox/commit/2b61da38c57dbfa5acdaf05c3835193b57f07569))
* gate the review queue behind an Experimental features row ([#457](https://github.com/usestring/gate-inbox/issues/457)) ([8bd6531](https://github.com/usestring/gate-inbox/commit/8bd6531416edfd875474b1b11a82c3a905804583))
* give review asks their own tab on the rail ([#458](https://github.com/usestring/gate-inbox/issues/458)) ([6475ddc](https://github.com/usestring/gate-inbox/commit/6475ddc478a7272b39cebdb7aa365e7faf4af576))
* **grant:** apply a parent's grants to an adopted Claude Code session live [stack 15/15] ([#279](https://github.com/usestring/gate-inbox/issues/279)) ([819d7c0](https://github.com/usestring/gate-inbox/commit/819d7c09e9a4f5878e6c362c608b6cea5573133c))
* **groups:** pin a group's directory from the CLI and create_group ([#490](https://github.com/usestring/gate-inbox/issues/490)) ([dbd3815](https://github.com/usestring/gate-inbox/commit/dbd3815b33c0deab4794bd9fae75e9e31624b3ee))
* **groups:** pin a group's path so every spawn into it starts there ([#401](https://github.com/usestring/gate-inbox/issues/401)) ([eafb5e2](https://github.com/usestring/gate-inbox/commit/eafb5e29399786959f9caf433de6edf6fbe560a9))
* **hooks:** bind an adopted claude row to its conversation from hook payloads [stack 3/15] ([#257](https://github.com/usestring/gate-inbox/issues/257)) ([9606b81](https://github.com/usestring/gate-inbox/commit/9606b81beb0488620e78568077c0cd9e9599f122))
* **hooks:** give adopted Claude Code sessions the board's steering [stack 5/15] ([#259](https://github.com/usestring/gate-inbox/issues/259)) ([2ee2003](https://github.com/usestring/gate-inbox/commit/2ee200334853291e41f19648e607d99de76aef30))
* **hooks:** give adopted Codex sessions status, steering and identity through global hooks [stack 10/15] ([#274](https://github.com/usestring/gate-inbox/issues/274)) ([7c6d12f](https://github.com/usestring/gate-inbox/commit/7c6d12fc493d4802c79d6fe05421ae195c3695e3))
* **hooks:** keep every Claude Code session out of the sealing keys [stack 7/15] ([#261](https://github.com/usestring/gate-inbox/issues/261)) ([d9aa1e3](https://github.com/usestring/gate-inbox/commit/d9aa1e3fda909c4c7e3937ad94931ecdef66d958))
* **hooks:** mark every adopted agent's pane, not only claude's [stack 8/15] ([#263](https://github.com/usestring/gate-inbox/issues/263)) ([83d55ab](https://github.com/usestring/gate-inbox/commit/83d55abaa7e8678e21fb21ef99d4fb26d17afb6b))
* **inbox:** add `inbox serve`, a remote inbox that starts router sessions ([#491](https://github.com/usestring/gate-inbox/issues/491)) ([cbc8669](https://github.com/usestring/gate-inbox/commit/cbc8669ff2b8ca44d21ad28b506f3d4b67fad968))
* **keys:** mute and unmute with ctrl+m on the list and inside a session ([#411](https://github.com/usestring/gate-inbox/issues/411)) ([ede60ef](https://github.com/usestring/gate-inbox/commit/ede60ef2b9a58ed6f9b69f68fbfe9b4104846d51))
* let agents report their own status, and read finishes past keep-alives ([#499](https://github.com/usestring/gate-inbox/issues/499)) ([b9e52b7](https://github.com/usestring/gate-inbox/commit/b9e52b74c9982e6ef01b51c109372e52ede23145))
* **mcp:** give adopted Claude Code sessions the board's tools ([#250](https://github.com/usestring/gate-inbox/issues/250)) ([bbb8f49](https://github.com/usestring/gate-inbox/commit/bbb8f49788067f10881e58d560afdd94b7f32e5a))
* **opencode:** give adopted opencode sessions their row, binding and steering [stack 11/15] ([#275](https://github.com/usestring/gate-inbox/issues/275)) ([dd5a0b6](https://github.com/usestring/gate-inbox/commit/dd5a0b6bf97e74ee35e45435f7261d8fdb9fc681))
* prepare a briefing session for each review ask before it is opened ([#468](https://github.com/usestring/gate-inbox/issues/468)) ([73eafc1](https://github.com/usestring/gate-inbox/commit/73eafc1a128147aa11190aa7a437031de74b4206))
* **reviews:** queue GitHub review asks in a Reviews row ([#435](https://github.com/usestring/gate-inbox/issues/435)) ([dbd051c](https://github.com/usestring/gate-inbox/commit/dbd051c17c1274dd8c38cfe95b6a194fe415e1b7))
* **reviews:** queue the Reviews row in triage by its oldest ask ([#438](https://github.com/usestring/gate-inbox/issues/438)) ([dff7ee8](https://github.com/usestring/gate-inbox/commit/dff7ee8bb4c02c61cb0e98b40edf5ddebdaabdf1))
* **scoring:** score subscription accounts by headroom ([#453](https://github.com/usestring/gate-inbox/issues/453)) ([f4adcf2](https://github.com/usestring/gate-inbox/commit/f4adcf2dddd2e42214e0eb061ca9720d0637621f))
* **setup:** keep Claude Code hooks and MCP relay registered with no command [stack 1/15] ([#255](https://github.com/usestring/gate-inbox/issues/255)) ([86a1426](https://github.com/usestring/gate-inbox/commit/86a1426eb58a607f04bd3b41a739bef96ec281f2))
* skip opencode's permission prompts through its generated config ([#450](https://github.com/usestring/gate-inbox/issues/450)) ([0b7e279](https://github.com/usestring/gate-inbox/commit/0b7e27991e50b8c8e5c323c1097b7bd24ea66f81))
* **status:** add a background tier for turns resting on a shell ([#446](https://github.com/usestring/gate-inbox/issues/446)) ([59db7b1](https://github.com/usestring/gate-inbox/commit/59db7b15a8845d2dd93d661a57f65477c5748764))
* **stop:** let a session archive its own row as it stops ([#423](https://github.com/usestring/gate-inbox/issues/423)) ([07e9f9d](https://github.com/usestring/gate-inbox/commit/07e9f9d06904d68cd883541f7bbd3d1c9070a3b1))
* switch idle operators to sessions needing attention ([#503](https://github.com/usestring/gate-inbox/issues/503)) ([02265bd](https://github.com/usestring/gate-inbox/commit/02265bd50dea334e2a479ad1379c8a40d0719630))
* **tmux:** give adopted panes the back-to-board key [stack 14/15] ([#278](https://github.com/usestring/gate-inbox/issues/278)) ([7c23721](https://github.com/usestring/gate-inbox/commit/7c2372110286c8dcca5c14770942e085ecce489a))
* **triage:** score smart triage as a sum of weighted terms ([#452](https://github.com/usestring/gate-inbox/issues/452)) ([40bb33d](https://github.com/usestring/gate-inbox/commit/40bb33d5c8de9ff02585a5202ba67e58fa9a35c7))
* **triage:** smart triage hands over the newest turn first ([#444](https://github.com/usestring/gate-inbox/issues/444)) ([083e193](https://github.com/usestring/gate-inbox/commit/083e193e62814e400211669e095e0efcc5b0360d))
* **ui:** ctrl+a attaches from a focused session ([#420](https://github.com/usestring/gate-inbox/issues/420)) ([8f15f6d](https://github.com/usestring/gate-inbox/commit/8f15f6d97163b92a6e3c02dfefa386b876eff9bd))
* **ui:** gray out agent CLIs not on PATH in the new-session pickers ([#451](https://github.com/usestring/gate-inbox/issues/451)) ([0628357](https://github.com/usestring/gate-inbox/commit/0628357a6cd40c2a52ab57c9999044162d0a0466))
* **ui:** JEV copy reads a turn on its key instead of queueing every turn end ([#410](https://github.com/usestring/gate-inbox/issues/410)) ([90fee4a](https://github.com/usestring/gate-inbox/commit/90fee4a9d4a8db263a67c28ee369f44929e996ba))
* **ui:** let an extension's header rows fold the session they head ([#412](https://github.com/usestring/gate-inbox/issues/412)) ([9fb22ff](https://github.com/usestring/gate-inbox/commit/9fb22ffaee5ed320e849d7af288a86b60a89d9f8))
* **ui:** make keeping arrows inside focused sessions a flag ([#493](https://github.com/usestring/gate-inbox/issues/493)) ([defba57](https://github.com/usestring/gate-inbox/commit/defba57e7159acb892b4ec6972aa6df7ade72f77))
* **ui:** match the row title in search ([#506](https://github.com/usestring/gate-inbox/issues/506)) ([c470d1c](https://github.com/usestring/gate-inbox/commit/c470d1ca57a52463a502d2b47604fdc5d16510c2))
* **ui:** offer recovery from subscription limits ([#481](https://github.com/usestring/gate-inbox/issues/481)) ([215b31d](https://github.com/usestring/gate-inbox/commit/215b31dd84e78c2db17cba925296ff0ed9f87209))
* **ui:** open a pull request or ticket row's link on click ([#441](https://github.com/usestring/gate-inbox/issues/441)) ([86ce93c](https://github.com/usestring/gate-inbox/commit/86ce93c7a90885a38b16f3060c8165ea12492eaa))
* **ui:** open the migrate card on auto when the build chooses CLIs ([#471](https://github.com/usestring/gate-inbox/issues/471)) ([767e446](https://github.com/usestring/gate-inbox/commit/767e4460a6b570af91709b7c5f9d175aeb8cf8dc))
* **ui:** open the switch-account card on auto when the build chooses accounts ([#477](https://github.com/usestring/gate-inbox/issues/477)) ([4a6016b](https://github.com/usestring/gate-inbox/commit/4a6016b04212803d1195ecdeccdc2c53c78ac105))
* **ui:** preview stopped sessions before reactivation ([#504](https://github.com/usestring/gate-inbox/issues/504)) ([6570b68](https://github.com/usestring/gate-inbox/commit/6570b680067c08c2ed23d71112c4efd330124d03))
* **ui:** queue text JEV expects you to copy from a turn's last message ([#382](https://github.com/usestring/gate-inbox/issues/382)) ([3d53948](https://github.com/usestring/gate-inbox/commit/3d5394895e18293e359745c886938b7c0b961183))
* **ui:** set the skip-permissions toggles when forking a session ([#449](https://github.com/usestring/gate-inbox/issues/449)) ([bcabec0](https://github.com/usestring/gate-inbox/commit/bcabec0da9cb1cad52b1fbb1f007e850cd3b9a4a))


### Bug Fixes

* **accounts:** resume urgent and high sessions in place up to their compact window ([#467](https://github.com/usestring/gate-inbox/issues/467)) ([f010d1e](https://github.com/usestring/gate-inbox/commit/f010d1e5fdcf67fabe7645bef89ac54437c1a56e))
* **adopt:** promote a dead adopted row when it is relaunched ([#434](https://github.com/usestring/gate-inbox/issues/434)) ([b52ffce](https://github.com/usestring/gate-inbox/commit/b52ffcea3a785fbe438046f2281c34f59c2376ab))
* **answer:** reach an adopted child's pane before answering its dialog [stack 2/15] ([#256](https://github.com/usestring/gate-inbox/issues/256)) ([f06ec3e](https://github.com/usestring/gate-inbox/commit/f06ec3e14d527f3d1503d011c72419233edccef3))
* backfill adopted Codex session prompts ([#486](https://github.com/usestring/gate-inbox/issues/486)) ([5455d2e](https://github.com/usestring/gate-inbox/commit/5455d2e51b3f47fb7c6d4e00c66308021981666e))
* bind new Codex sessions by creation time ([#492](https://github.com/usestring/gate-inbox/issues/492)) ([f159339](https://github.com/usestring/gate-inbox/commit/f159339ab483397c4649586b424c86744b206108))
* **childsweep:** don't archive a restarting parent's finished children ([#414](https://github.com/usestring/gate-inbox/issues/414)) ([ad1e11e](https://github.com/usestring/gate-inbox/commit/ad1e11e99e4b8bfe9bceb9b560c8589b0b6b6b9e))
* **codex:** bind structured replies to their questions ([#454](https://github.com/usestring/gate-inbox/issues/454)) ([8cd2f40](https://github.com/usestring/gate-inbox/commit/8cd2f400d7dbb4545b52b0e66997545d544277a4))
* **codex:** hold messages during hook trust review ([#421](https://github.com/usestring/gate-inbox/issues/421)) ([2b20394](https://github.com/usestring/gate-inbox/commit/2b20394bde8dd8c879d55f21d05de595c7951285))
* **codex:** preserve approval evidence across resumes ([#427](https://github.com/usestring/gate-inbox/issues/427)) ([d18e53f](https://github.com/usestring/gate-inbox/commit/d18e53f8787aaa69bbf0f64370155a456e74ebdd))
* **codex:** type queued messages into a working Codex session ([#426](https://github.com/usestring/gate-inbox/issues/426)) ([8f339e2](https://github.com/usestring/gate-inbox/commit/8f339e2c33879a901a88d00d3735f93d762b4013))
* **compact:** give child sessions the compact window their parent's tier earns ([#416](https://github.com/usestring/gate-inbox/issues/416)) ([e29342c](https://github.com/usestring/gate-inbox/commit/e29342cc499282cf9bb11af59380eb980e07c577))
* **copy:** recognize draft replies and add historical evals ([#501](https://github.com/usestring/gate-inbox/issues/501)) ([1f5e08a](https://github.com/usestring/gate-inbox/commit/1f5e08a0ae922084d8f8727aa77bd2192105d205))
* **focus:** let Right leave a Claude prompt showing its suggestion ([#394](https://github.com/usestring/gate-inbox/issues/394)) ([c8398a0](https://github.com/usestring/gate-inbox/commit/c8398a0a66cf077464049a5848964adea2d60c9e))
* **mcpserver:** withdraw a spawn whose create_session call was cancelled ([#407](https://github.com/usestring/gate-inbox/issues/407)) ([fb24bbb](https://github.com/usestring/gate-inbox/commit/fb24bbb21e0895c29344fac43af363ab41910dc4))
* **migrate:** hand a migrated session's children, priority and spawner to the new session and retire the source ([#413](https://github.com/usestring/gate-inbox/issues/413)) ([a2494b3](https://github.com/usestring/gate-inbox/commit/a2494b3c362836ef7bc025b3dbb60eb94f2322ee))
* **migrate:** keep the source on a manual migration; retire it only on an account switch ([#456](https://github.com/usestring/gate-inbox/issues/456)) ([408ea67](https://github.com/usestring/gate-inbox/commit/408ea67b851840caebafc5e5f72368ae6c2a8e79))
* **relay:** accept human answers to Codex async questions ([#418](https://github.com/usestring/gate-inbox/issues/418)) ([358b7c0](https://github.com/usestring/gate-inbox/commit/358b7c0f0153f1f2bdf2ca7bb5148093fc12823a))
* retry Codex capacity failures with bounded backoff ([#479](https://github.com/usestring/gate-inbox/issues/479)) ([ad07d74](https://github.com/usestring/gate-inbox/commit/ad07d743253ff64b2b87ead3e5aba23b7b0063b1))
* **sessioncmd:** reach adopted panes before every liveness read [stack 4/15] ([#258](https://github.com/usestring/gate-inbox/issues/258)) ([0897cb7](https://github.com/usestring/gate-inbox/commit/0897cb76c50eb1beed3bfda216a6a6c7703dc0d9))
* **sessioncmd:** verify shared Codex stop callers through their pane ([#476](https://github.com/usestring/gate-inbox/issues/476)) ([9973fc3](https://github.com/usestring/gate-inbox/commit/9973fc344ea6a0f951394f4bbd8f2d0023fcb781))
* **spawn:** divert a spawn into a nested repository past the trust dialog ([#400](https://github.com/usestring/gate-inbox/issues/400)) ([c976442](https://github.com/usestring/gate-inbox/commit/c976442c0c03957e4ffaa62d74040a548bf46c73))
* **spawn:** open a spawn in place when its CLI already trusts the directory ([#489](https://github.com/usestring/gate-inbox/issues/489)) ([b2a2453](https://github.com/usestring/gate-inbox/commit/b2a24538e476e452b45554855752004f0545b5e1))
* **status:** don't tell a parent its child finished when the board is about to restart it ([#405](https://github.com/usestring/gate-inbox/issues/405)) ([2df20ec](https://github.com/usestring/gate-inbox/commit/2df20ecaa6c8299720a5f39e5700c48346b027bf))
* **status:** read a session as working on the pass that types its pending prompt ([#406](https://github.com/usestring/gate-inbox/issues/406)) ([a0ef749](https://github.com/usestring/gate-inbox/commit/a0ef749bfc51e02431ea37d0a177cc4978c1afb3))
* **test:** compare resolved terminal fallback paths ([#484](https://github.com/usestring/gate-inbox/issues/484)) ([43b8481](https://github.com/usestring/gate-inbox/commit/43b84814f8c804976c6bbb7fc207a746a4cbfe80))
* **test:** make adoption timestamps deterministic ([#485](https://github.com/usestring/gate-inbox/issues/485)) ([7a82061](https://github.com/usestring/gate-inbox/commit/7a8206172e7598f1bd77feccebf8e0dad315fb25))
* **tmux:** filter macOS MallocStackLogging noise out of Claude panes ([#437](https://github.com/usestring/gate-inbox/issues/437)) ([5ae6fa2](https://github.com/usestring/gate-inbox/commit/5ae6fa2a59096120f0a85fd23f4eaa85f579504e))
* **tmux:** keep the board's own window following its terminal ([#464](https://github.com/usestring/gate-inbox/issues/464)) ([dfde057](https://github.com/usestring/gate-inbox/commit/dfde0572e18ee4ed80fbb145fc12e800925376b9))
* **tmux:** refuse submission without a dialog check ([#431](https://github.com/usestring/gate-inbox/issues/431)) ([7bb9dca](https://github.com/usestring/gate-inbox/commit/7bb9dca20be20f2ed01908c5eac80206a519581e))
* **triage:** hand over higher-priority sessions first ([#432](https://github.com/usestring/gate-inbox/issues/432)) ([c64d5a4](https://github.com/usestring/gate-inbox/commit/c64d5a40549fe3923d5b521cc5dc62a1b8cf48a9))
* **triage:** order by state without a submission cooldown ([#465](https://github.com/usestring/gate-inbox/issues/465)) ([86a2980](https://github.com/usestring/gate-inbox/commit/86a29807900f6b0dbf1f0872754d284c219feb55))
* **triage:** refresh submitted sessions before reoffering them ([#473](https://github.com/usestring/gate-inbox/issues/473)) ([31fdca7](https://github.com/usestring/gate-inbox/commit/31fdca7923619fd3b6e3b5d952bee6a485cdb497))
* **triage:** widen a drained scope instead of stopping on the list ([#463](https://github.com/usestring/gate-inbox/issues/463)) ([f338d7d](https://github.com/usestring/gate-inbox/commit/f338d7d89e7683fa1ec816d625cb755e44bf60db))
* **ui:** clear search when navigating to a session ([#483](https://github.com/usestring/gate-inbox/issues/483)) ([c496aba](https://github.com/usestring/gate-inbox/commit/c496abad36d2605dfcd0a0a44cc0d5fd2fca450d))
* **ui:** copy links to the status bar instead of suspending the board over SSH ([#469](https://github.com/usestring/gate-inbox/issues/469)) ([7668a85](https://github.com/usestring/gate-inbox/commit/7668a8571635b931708244b89e279f54f0764f12))
* **ui:** respect priority while background monitors run ([#498](https://github.com/usestring/gate-inbox/issues/498)) ([5071318](https://github.com/usestring/gate-inbox/commit/50713186d1b82af574980d67017d6c447b57b49b))
* **ui:** reword the JEV finish question and ask the agent when unsure ([#497](https://github.com/usestring/gate-inbox/issues/497)) ([15467e3](https://github.com/usestring/gate-inbox/commit/15467e37b7d5c2ca8ab7080c1771a817c207e8b5))
* **ui:** show a screen's own submit key once in the field footer ([#404](https://github.com/usestring/gate-inbox/issues/404)) ([9cf0611](https://github.com/usestring/gate-inbox/commit/9cf0611bb56a9c1718f214c5f6a147e4a0a2a276))
* **ui:** surface parents waiting on session results ([#494](https://github.com/usestring/gate-inbox/issues/494)) ([7b3d33c](https://github.com/usestring/gate-inbox/commit/7b3d33c538206419a9635342c7b5fcef135c538f))
* **ui:** wait for dialog answers before advancing ([#480](https://github.com/usestring/gate-inbox/issues/480)) ([e72a0a1](https://github.com/usestring/gate-inbox/commit/e72a0a174463189efeb7cbd413de4a8f5d8c72dc))


### Performance Improvements

* **sessioncmd:** read only live overlay columns for a resting session ([#461](https://github.com/usestring/gate-inbox/issues/461)) ([fe60ec8](https://github.com/usestring/gate-inbox/commit/fe60ec879b4eefd0f2610ed80db6fb72e6d5eebc))
* **sessioncmd:** reuse store handles across the board's reads ([#462](https://github.com/usestring/gate-inbox/issues/462)) ([6602eec](https://github.com/usestring/gate-inbox/commit/6602eece3c07b60e94e5b716ac0390327815464b))

## [1.0.0](https://github.com/usestring/gate-inbox/compare/v0.12.0...v1.0.0) (2026-10-21)


### ⚠ BREAKING CHANGES

* remove the shared-key artifacts extension and its Worker ([#388](https://github.com/usestring/gate-inbox/issues/388))
* **keymap:** free option letters for snippet chords ([#367](https://github.com/usestring/gate-inbox/issues/367))

### Features

* add a 'what do you need from me' snippet ([#321](https://github.com/usestring/gate-inbox/issues/321)) ([f95591b](https://github.com/usestring/gate-inbox/commit/f95591b8145dff9f1f20be0fe02b06688d9137f6))
* add reusable skill priority hook ([#365](https://github.com/usestring/gate-inbox/issues/365)) ([0558c4d](https://github.com/usestring/gate-inbox/commit/0558c4d57fb9b80b320cce7fe83aab8603e7af40))
* **answer:** hand parents the exact relay input and retry an answer that failed to land ([#353](https://github.com/usestring/gate-inbox/issues/353)) ([24284ff](https://github.com/usestring/gate-inbox/commit/24284ff8ac42aca498cb4d4ec2149dc6d4977358))
* **capture:** record the provenance of every capture in capture.json ([#335](https://github.com/usestring/gate-inbox/issues/335)) ([b02ca7c](https://github.com/usestring/gate-inbox/commit/b02ca7cc417bb4806b199068cea56df66028c9a0))
* **claude:** add the relay-child-dialog skill ([#354](https://github.com/usestring/gate-inbox/issues/354)) ([bdd8c6b](https://github.com/usestring/gate-inbox/commit/bdd8c6ba14600e9ceb1c9a2f66e7cb5921bc3851))
* **claude:** load Gate Inbox into Claude Code as one versioned plugin ([#344](https://github.com/usestring/gate-inbox/issues/344)) ([60902d2](https://github.com/usestring/gate-inbox/commit/60902d2848ba453ef8ede715a8f489eec307de62))
* **claude:** steer write-capable Agent calls to create_session ([#348](https://github.com/usestring/gate-inbox/issues/348)) ([08cf55c](https://github.com/usestring/gate-inbox/commit/08cf55c799037d467bbe9ed0decb7fb1c0228ddf))
* **cli:** list your children and the dialogs they wait on ([#336](https://github.com/usestring/gate-inbox/issues/336)) ([fc01215](https://github.com/usestring/gate-inbox/commit/fc01215b2654b3b99daa960a699af6fe5f9864d5))
* **cli:** propose cleanups for stale board rows with gate-inbox sweep and /gi-sweep ([#349](https://github.com/usestring/gate-inbox/issues/349)) ([cce7f27](https://github.com/usestring/gate-inbox/commit/cce7f272919b7e814e939cf1a964d2a32212f7d1))
* **extension:** let a command spend its own session's user approval once ([#334](https://github.com/usestring/gate-inbox/issues/334)) ([c2af4c2](https://github.com/usestring/gate-inbox/commit/c2af4c2cc636e47235b2caabd4be004a56805fc9))
* **grant:** approve a step's commands with one question and carry its instruction ([#295](https://github.com/usestring/gate-inbox/issues/295)) ([5755e96](https://github.com/usestring/gate-inbox/commit/5755e9618fa68ef2578ffcc07c7ebfca654f45f9))
* **grant:** let an approved grant stand for every session until revoked ([#312](https://github.com/usestring/gate-inbox/issues/312)) ([c01ab5d](https://github.com/usestring/gate-inbox/commit/c01ab5d90b110c3d7e38e723e2cb8d43932c9e8b))
* gray the focused pane and tint the dock while the hotkey menu holds the keys ([#364](https://github.com/usestring/gate-inbox/issues/364)) ([9685f93](https://github.com/usestring/gate-inbox/commit/9685f93012f1bea9c4e1b096cde30ceea1d8027c))
* **hooks:** register Claude Code hooks globally for adopted sessions ([#249](https://github.com/usestring/gate-inbox/issues/249)) ([c0aa436](https://github.com/usestring/gate-inbox/commit/c0aa436f032a5a13e25eb641f48e2bce5aa3fe26))
* **keymap:** add Ctrl+M as a mute shortcut ([#320](https://github.com/usestring/gate-inbox/issues/320)) ([c1d7f21](https://github.com/usestring/gate-inbox/commit/c1d7f216592a9aca028bf1f3c5a3f3e40035fc02))
* **keymap:** free option letters for snippet chords ([#367](https://github.com/usestring/gate-inbox/issues/367)) ([f6141b1](https://github.com/usestring/gate-inbox/commit/f6141b107e22d13cdefb06565d86c9ae35ec8023))
* **launch:** pace session relaunches through one shared queue ([#398](https://github.com/usestring/gate-inbox/issues/398)) ([cbe7ad9](https://github.com/usestring/gate-inbox/commit/cbe7ad911b7f5fd36b5879845a485d42c302158b))
* **mcp:** serve core and admin tool profiles; give spawned children core ([#343](https://github.com/usestring/gate-inbox/issues/343)) ([5da1931](https://github.com/usestring/gate-inbox/commit/5da19311ca896a4889ce7875bac88825c8db39e8))
* **merge-gate:** let covered squash merges pass the classifier via a mod ([#332](https://github.com/usestring/gate-inbox/issues/332)) ([d8abc3c](https://github.com/usestring/gate-inbox/commit/d8abc3ce18bbd77b8743ae078b9654280a0811f6))
* **priority:** scale claude's auto-compact window by tier ([#395](https://github.com/usestring/gate-inbox/issues/395)) ([a3d85f5](https://github.com/usestring/gate-inbox/commit/a3d85f572edefcc5512d79da581708f9a610498a))
* **relay:** put a child's permission prompt to the user as a decision ([#377](https://github.com/usestring/gate-inbox/issues/377)) ([dbfb392](https://github.com/usestring/gate-inbox/commit/dbfb3921ac298d9afd29cc547049e09e4cb651a0))
* remove the shared-key artifacts extension and its Worker ([#388](https://github.com/usestring/gate-inbox/issues/388)) ([e802148](https://github.com/usestring/gate-inbox/commit/e8021489456171431947ca09a41a6b21c5d7e975))
* **sessioncmd:** restore or revive the sender's own archived child on send ([#342](https://github.com/usestring/gate-inbox/issues/342)) ([0a404b1](https://github.com/usestring/gate-inbox/commit/0a404b1ec7812f5557f9ed127902c2c12558953c))
* show a notice for typing blocked by the snippets menu, open it on ctrl+s ([#363](https://github.com/usestring/gate-inbox/issues/363)) ([a6c66d9](https://github.com/usestring/gate-inbox/commit/a6c66d97123a9576b3f9a94fb78b1ff61967abc2))
* show muted sessions in triage, sorted last ([#330](https://github.com/usestring/gate-inbox/issues/330)) ([12ee6fc](https://github.com/usestring/gate-inbox/commit/12ee6fc81d0c54b44a0b34d445ed03950e366079))
* **snippets:** add a 'use your judgment' snippet ([#369](https://github.com/usestring/gate-inbox/issues/369)) ([7c3ec44](https://github.com/usestring/gate-inbox/commit/7c3ec4439fb9f2a6637f1ffa730aea596194c08c))
* **snippets:** allow custom menu previews ([#317](https://github.com/usestring/gate-inbox/issues/317)) ([be7e635](https://github.com/usestring/gate-inbox/commit/be7e6353828012bc604f90f58829b9b3ba6d4b1b))
* **snippets:** name the direct chord in snippets.json ([#243](https://github.com/usestring/gate-inbox/issues/243)) ([7ce640e](https://github.com/usestring/gate-inbox/commit/7ce640e54258df3a8362a45489285c279bd2aa85))
* **snippets:** show labels in the hotkey menu and move the progress default to p ([#350](https://github.com/usestring/gate-inbox/issues/350)) ([7192551](https://github.com/usestring/gate-inbox/commit/71925511da18b9c71d75f4234b875738e91b7762))
* **triage:** hand the drain over on Enter without waiting for the answer to land ([#370](https://github.com/usestring/gate-inbox/issues/370)) ([583744b](https://github.com/usestring/gate-inbox/commit/583744b3b5e793430dba2e52d25a98723a602e49))
* **triage:** show a child's Approval question to the operator at once ([#296](https://github.com/usestring/gate-inbox/issues/296)) ([7a41afe](https://github.com/usestring/gate-inbox/commit/7a41afe8e5ebb4714ec5d94e8b32ac9c1bd5c879))
* **triage:** smart triage orders the queue by weighted wait ([#381](https://github.com/usestring/gate-inbox/issues/381)) ([0ddca3f](https://github.com/usestring/gate-inbox/commit/0ddca3f4b839947fc1a718c68209be1e68996e96))
* **ui:** add a JEV settings card with key paste and access status ([#239](https://github.com/usestring/gate-inbox/issues/239)) ([8930b07](https://github.com/usestring/gate-inbox/commit/8930b07a825a0c50bf18180bc513b04d2c01d1d1))
* **ui:** add restore all and make ctrl+x kill from anywhere ([#390](https://github.com/usestring/gate-inbox/issues/390)) ([cc34db0](https://github.com/usestring/gate-inbox/commit/cc34db0d81ee8d0171686e86f3016568ae30a583))
* **ui:** ask before a key map rebind takes another action's key ([#281](https://github.com/usestring/gate-inbox/issues/281)) ([2666bc0](https://github.com/usestring/gate-inbox/commit/2666bc05c28baaf0524dbcd298fb6a2657558d21))
* **ui:** draw an extension's header page in the session pane ([#291](https://github.com/usestring/gate-inbox/issues/291)) ([1c4ba70](https://github.com/usestring/gate-inbox/commit/1c4ba704b4d25e9f3759cf391c8d9c4a67ef56c8))
* **ui:** let an extension's header be a row the board opens ([#284](https://github.com/usestring/gate-inbox/issues/284)) ([6566689](https://github.com/usestring/gate-inbox/commit/656668968cee8062db233f450fadea9b60b5bdb9))
* **ui:** list every account on the switch-account card ([#265](https://github.com/usestring/gate-inbox/issues/265)) ([d5e17ab](https://github.com/usestring/gate-inbox/commit/d5e17abf142192c5036beee822890cde86bf6a1b))
* **ui:** make ctrl+a the only attach key on the list ([#383](https://github.com/usestring/gate-inbox/issues/383)) ([bee8705](https://github.com/usestring/gate-inbox/commit/bee8705057fc53df692911ec52f24d263b7e6b27))
* **ui:** mark the open hotkey menu with a red banner instead of graying the pane ([#371](https://github.com/usestring/gate-inbox/issues/371)) ([3ceb3cf](https://github.com/usestring/gate-inbox/commit/3ceb3cf13b6d1129c273127a1eb53136b2e23335))
* **ui:** open the new-session box on auto's pick ([#287](https://github.com/usestring/gate-inbox/issues/287)) ([84ad41b](https://github.com/usestring/gate-inbox/commit/84ad41bfce2f83e35a43318d3fde20fe01b169c3))
* **ui:** record every answer's landing, its source and its cost ([#386](https://github.com/usestring/gate-inbox/issues/386)) ([5c4625a](https://github.com/usestring/gate-inbox/commit/5c4625ad48fbb987fd5692b397fa239bb8b53b02))
* **ui:** send a lettered snippet on a direct option+shift chord ([#242](https://github.com/usestring/gate-inbox/issues/242)) ([d5e5ea9](https://github.com/usestring/gate-inbox/commit/d5e5ea9643cf3a151aaba2af35d97a356632d0c8))
* **ui:** show a delegating state for sessions waiting on their children ([#323](https://github.com/usestring/gate-inbox/issues/323)) ([b226a49](https://github.com/usestring/gate-inbox/commit/b226a496bd9928a7d5beb6af98c0b9fb7071ebb4))
* **ui:** show the last-pane hotkey in the default toolbar ([#234](https://github.com/usestring/gate-inbox/issues/234)) ([e2adc09](https://github.com/usestring/gate-inbox/commit/e2adc096d2b01763b7ab1d6866fce991f8b1a9d8))
* **ui:** show when the selected session started ([#264](https://github.com/usestring/gate-inbox/issues/264)) ([684b98a](https://github.com/usestring/gate-inbox/commit/684b98aaf9573ca451ec7db100397d5818f93848))
* **ui:** show why auto picked a new session's CLI ([#333](https://github.com/usestring/gate-inbox/issues/333)) ([df23c6c](https://github.com/usestring/gate-inbox/commit/df23c6c6c89981cbd815a67a79c3e0e60b6eece9))
* **ui:** start a new session with ctrl+n from anywhere ([#288](https://github.com/usestring/gate-inbox/issues/288)) ([57b1119](https://github.com/usestring/gate-inbox/commit/57b111931469f127a0d93735f9e0fb6b666333bc))


### Bug Fixes

* **adopt:** identify Codex behind its client wrapper ([#358](https://github.com/usestring/gate-inbox/issues/358)) ([29aa08e](https://github.com/usestring/gate-inbox/commit/29aa08e4c844e3422f9925569686455de664b844))
* **capture:** refuse the live board and record only a scratch home and fixture project ([#293](https://github.com/usestring/gate-inbox/issues/293)) ([2aaecb2](https://github.com/usestring/gate-inbox/commit/2aaecb2da93dadceb721c6e610708e9938c2b55c))
* **capture:** send Ctrl+S in the hotkey-menu tape ([#389](https://github.com/usestring/gate-inbox/issues/389)) ([756b43e](https://github.com/usestring/gate-inbox/commit/756b43ecba813fd41a40ba8eeec38f530ea83525))
* **ci:** keep main green after the harness filter landed ([#270](https://github.com/usestring/gate-inbox/issues/270)) ([d49891f](https://github.com/usestring/gate-inbox/commit/d49891f67cbd15cb2d69eb4ffb8a9207539cff22))
* **cli:** verify caller ownership before stopping ([#310](https://github.com/usestring/gate-inbox/issues/310)) ([d5807e6](https://github.com/usestring/gate-inbox/commit/d5807e62e6f96827fabfa4f95936e15ffdf71f2c))
* **config:** let the /model effort slider keep Left and Right ([#351](https://github.com/usestring/gate-inbox/issues/351)) ([dcfc3c6](https://github.com/usestring/gate-inbox/commit/dcfc3c64e6d172875360a1510f058a8c70bb68db))
* **convo:** keep the board's own messages out of drift naming ([#272](https://github.com/usestring/gate-inbox/issues/272)) ([790a126](https://github.com/usestring/gate-inbox/commit/790a1268271876704982fef230eed70e935c3544))
* **convo:** show the slash command a session was opened with ([#384](https://github.com/usestring/gate-inbox/issues/384)) ([f60419d](https://github.com/usestring/gate-inbox/commit/f60419dd9f38c035a462329441419ef000d60b9c))
* **deps:** build with Go 1.27.3 for the crypto/x509 advisory ([#403](https://github.com/usestring/gate-inbox/issues/403)) ([48d69ef](https://github.com/usestring/gate-inbox/commit/48d69ef6bce5592b1a01a410892ddad09456f99d))
* **dialog:** keep a permission prompt's auto-deny countdown out of its identity ([#329](https://github.com/usestring/gate-inbox/issues/329)) ([f6844ef](https://github.com/usestring/gate-inbox/commit/f6844efd861387d7c7a935def94dd1ffc7b08cd4))
* **dialog:** relay a question taller than its dialog from the child's own call ([#379](https://github.com/usestring/gate-inbox/issues/379)) ([6d46409](https://github.com/usestring/gate-inbox/commit/6d4640969c4321f50f79bcdede4edcf968b097b7))
* **dialog:** reuse a user's answer when nothing was keyed ([#331](https://github.com/usestring/gate-inbox/issues/331)) ([9c09854](https://github.com/usestring/gate-inbox/commit/9c09854b316a9383d3edda75b42b304c1a869ea6))
* end managed sessions through an intentional self-stop ([#244](https://github.com/usestring/gate-inbox/issues/244)) ([6c72f34](https://github.com/usestring/gate-inbox/commit/6c72f34913d0e824871cb66ba80c26b48fe5e123))
* **grant:** keep a grant restart from being filed as an exited child ([#294](https://github.com/usestring/gate-inbox/issues/294)) ([8854e74](https://github.com/usestring/gate-inbox/commit/8854e744c96436b5133bbab773b00974306c326d))
* **hooks:** keep a child in its sandbox when a bound path vanishes ([#311](https://github.com/usestring/gate-inbox/issues/311)) ([9ccf15b](https://github.com/usestring/gate-inbox/commit/9ccf15b0ced1b9b01ce7d65fa6a3747b87d8a376))
* **hooks:** keep restored sandbox placeholders alive across other sessions' exits ([#359](https://github.com/usestring/gate-inbox/issues/359)) ([cc26314](https://github.com/usestring/gate-inbox/commit/cc2631479d227d61a924892dee1d8d2cece5f568))
* **hooks:** put a removed worktree's metadata back so a stale sandbox recovers in place ([#328](https://github.com/usestring/gate-inbox/issues/328)) ([c6b93b1](https://github.com/usestring/gate-inbox/commit/c6b93b10c7a328d02916e2b0f63887118f45855b))
* **hooks:** show a compacting Claude session as working ([#392](https://github.com/usestring/gate-inbox/issues/392)) ([3ae5cc6](https://github.com/usestring/gate-inbox/commit/3ae5cc64bc2d35d7f87dd8e9ba73bfde7eec52f2))
* **launch:** keep a managed claude's permission prompt standing for its relay ([#378](https://github.com/usestring/gate-inbox/issues/378)) ([a79d831](https://github.com/usestring/gate-inbox/commit/a79d8310a1e5879cb02562a14dbf1088cac72cac))
* list every account on the switch-account card ([#282](https://github.com/usestring/gate-inbox/issues/282)) ([5217c3d](https://github.com/usestring/gate-inbox/commit/5217c3d2dcb61c73b9fd664632d98c11f5aad368))
* **rail:** scroll the session list only when the cursor reaches an edge ([#303](https://github.com/usestring/gate-inbox/issues/303)) ([0bddeda](https://github.com/usestring/gate-inbox/commit/0bddeda6e5a6ec6252dbfb861e224a6b4c135a48))
* refuse an attach into the manager's own session ([#235](https://github.com/usestring/gate-inbox/issues/235)) ([519a890](https://github.com/usestring/gate-inbox/commit/519a890cc1308dd31f50f68c7885af6a3e85ccb5))
* **reorder:** keep a moved row in place when a stale poll lands ([#304](https://github.com/usestring/gate-inbox/issues/304)) ([51cf3ea](https://github.com/usestring/gate-inbox/commit/51cf3ea0184b4b163eb7ee514baef8376a8ab447))
* **scripts:** strip BSD wc padding from shard-test counts ([#326](https://github.com/usestring/gate-inbox/issues/326)) ([23ccec0](https://github.com/usestring/gate-inbox/commit/23ccec0b666441b13fa0a431cb18e6b365dd3142))
* **search:** record a PR the wrapper confirmed inside a script or background job ([#245](https://github.com/usestring/gate-inbox/issues/245)) ([45b3254](https://github.com/usestring/gate-inbox/commit/45b3254a8964157e4fb9a9d28b9bf40513d1fc11))
* **sessioncmd:** read an archived dead row without asking tmux about its pane ([#337](https://github.com/usestring/gate-inbox/issues/337)) ([f61b180](https://github.com/usestring/gate-inbox/commit/f61b18039f9632a0ff0bbb5c5dd785dacd5a7eb9))
* **sessioncmd:** read any dead row without asking tmux about its pane ([#399](https://github.com/usestring/gate-inbox/issues/399)) ([ce13bd1](https://github.com/usestring/gate-inbox/commit/ce13bd185d43350838ed2718ec9bf1abfb92e227))
* **status:** hold queued messages on a Claude question whose legend wraps ([#308](https://github.com/usestring/gate-inbox/issues/308)) ([1ec3334](https://github.com/usestring/gate-inbox/commit/1ec3334114ca975bb8b5fa2541d1b92ecddc262e))
* **status:** read a Codex turn closed by a bulleted Worked-for label as finished ([#376](https://github.com/usestring/gate-inbox/issues/376)) ([0284ad1](https://github.com/usestring/gate-inbox/commit/0284ad1875b02ac9d2e126601cdd5d6cbcb5ee2e))
* **status:** read a transcript's quiet time from its last entry, not its mtime ([#361](https://github.com/usestring/gate-inbox/issues/361)) ([f548234](https://github.com/usestring/gate-inbox/commit/f548234e65b31f6956f17495f582cecccd29676f))
* **status:** read a turn that ends in a prose question as finished ([#360](https://github.com/usestring/gate-inbox/issues/360)) ([7d72101](https://github.com/usestring/gate-inbox/commit/7d7210111f6d4014ba0a9f2e6a4f4d56bf5f9c90))
* **status:** read Claude's /clear hint under a turn-end summary as chrome ([#366](https://github.com/usestring/gate-inbox/issues/366)) ([7f176e3](https://github.com/usestring/gate-inbox/commit/7f176e3d5e97a3e6381d3751ee0aec840571ecde))
* **status:** read failed Codex reconnects as errored under older configs ([#375](https://github.com/usestring/gate-inbox/issues/375)) ([201137d](https://github.com/usestring/gate-inbox/commit/201137d2294fcd9c90d3dc34d293fe6b4b4642d7))
* **status:** restore an archived row whose agent is running ([#357](https://github.com/usestring/gate-inbox/issues/357)) ([6a284d1](https://github.com/usestring/gate-inbox/commit/6a284d1bc0b0debb236f7f7b82c714bf1a213a6d))
* **status:** stop keep-warm pins and stale background tails from misreporting rest ([#345](https://github.com/usestring/gate-inbox/issues/345)) ([105f513](https://github.com/usestring/gate-inbox/commit/105f51369784fa0217f30d35a1f4024cd4e697ae))
* **steering:** tell a child not to ask for what it already has ([#313](https://github.com/usestring/gate-inbox/issues/313)) ([ff477d0](https://github.com/usestring/gate-inbox/commit/ff477d00013c8dfb6fd2b9b72a617b3dded5402b))
* **store:** exempt board extensions from the pair cap ([#300](https://github.com/usestring/gate-inbox/issues/300)) ([f60664f](https://github.com/usestring/gate-inbox/commit/f60664fd9541398e7737479d668a5b9610589343))
* **sweep:** retire acknowledged and orphaned children, and never leave a relaunched agent archived ([#347](https://github.com/usestring/gate-inbox/issues/347)) ([0907198](https://github.com/usestring/gate-inbox/commit/09071986c3127fc300162dac55040b6f0442d297))
* switch the operator's client instead of nesting an attach on the same server ([#271](https://github.com/usestring/gate-inbox/issues/271)) ([a702d24](https://github.com/usestring/gate-inbox/commit/a702d24eea9e13d08b0bb646b973b0fb453821f9))
* **tests:** keep fixture git calls out of the ambient checkout ([#325](https://github.com/usestring/gate-inbox/issues/325)) ([05fd289](https://github.com/usestring/gate-inbox/commit/05fd2892bacb54f5fc5a8cfe7b67a51936391a57))
* **tests:** prove the codex seed stays off the pass without a clock ([#327](https://github.com/usestring/gate-inbox/issues/327)) ([e731bfd](https://github.com/usestring/gate-inbox/commit/e731bfd277be7a81a26a627d2948f1601f930114))
* **tmux:** detach on ctrl+q and ctrl+\ only from a managed session's home window ([#362](https://github.com/usestring/gate-inbox/issues/362)) ([53fc94a](https://github.com/usestring/gate-inbox/commit/53fc94aafc4bada92b311153bad025474524c5b5))
* **tmux:** look for a dialog before pressing Enter on a delivered paste ([#309](https://github.com/usestring/gate-inbox/issues/309)) ([de091be](https://github.com/usestring/gate-inbox/commit/de091be116af6d016f8e66cf24c4396854af6e08))
* **tmuxtest:** unset the live session's GATE_INBOX_* variables in tests ([#238](https://github.com/usestring/gate-inbox/issues/238)) ([b31729f](https://github.com/usestring/gate-inbox/commit/b31729f8de6a3d016a494aed48f722356bfb75d1))
* **triage:** count smart triage waits from the operator's last turn ([#396](https://github.com/usestring/gate-inbox/issues/396)) ([2dd3513](https://github.com/usestring/gate-inbox/commit/2dd35132543ae88ff6c3b8335434d9e8d06ef885))
* **triage:** keep the drain on a dialog until Enter ends it ([#373](https://github.com/usestring/gate-inbox/issues/373)) ([154e151](https://github.com/usestring/gate-inbox/commit/154e1514f80bda299502bb862cfb3b38f8c22173))
* **triage:** let a skipped session rejoin the queue after 15 minutes ([#397](https://github.com/usestring/gate-inbox/issues/397)) ([c4bfe7d](https://github.com/usestring/gate-inbox/commit/c4bfe7d1ad98f1127835c9e437a13f152c75fb70))
* **ui:** back off a composer draft that will not clear, and tell the operator ([#341](https://github.com/usestring/gate-inbox/issues/341)) ([324a2fc](https://github.com/usestring/gate-inbox/commit/324a2fc962c78953c92badab8fa21e78c0acfb1f))
* **ui:** cap sharded test processes machine-wide at one per core ([#246](https://github.com/usestring/gate-inbox/issues/246)) ([f25b2e3](https://github.com/usestring/gate-inbox/commit/f25b2e3e55f4f4308fe43d1a36c6fb32799941a3))
* **ui:** check triage answers immediately in the current pane ([#318](https://github.com/usestring/gate-inbox/issues/318)) ([c1536a3](https://github.com/usestring/gate-inbox/commit/c1536a3a11a74080271a1d2e785cd78e8739ea1e))
* **ui:** detach triage nesting across mute partitions ([#356](https://github.com/usestring/gate-inbox/issues/356)) ([9541ff7](https://github.com/usestring/gate-inbox/commit/9541ff7850095e9c98bf5f5e5057330a0b2ed364))
* **ui:** draw a header with a page once, as its row ([#299](https://github.com/usestring/gate-inbox/issues/299)) ([dd3cd62](https://github.com/usestring/gate-inbox/commit/dd3cd62d0ae0f7533b07a1ae31225ee26fafe764))
* **ui:** hand errored sessions over last in triage ([#266](https://github.com/usestring/gate-inbox/issues/266)) ([542c638](https://github.com/usestring/gate-inbox/commit/542c638afc037f51f3b9d78abe3fe914c016304e))
* **ui:** hold a child's dialog relay while the parent is on its own dialog ([#380](https://github.com/usestring/gate-inbox/issues/380)) ([05d3f7a](https://github.com/usestring/gate-inbox/commit/05d3f7aaf9ec217c72067e157d6a2c63d1dc35f9))
* **ui:** leave focus on either prompt-edge arrow ([#253](https://github.com/usestring/gate-inbox/issues/253)) ([3ad628e](https://github.com/usestring/gate-inbox/commit/3ad628e974f637d6ccaa354abf59f7610b76f90a))
* **ui:** log board keystrokes at debug, keep the mode change a key causes at info ([#339](https://github.com/usestring/gate-inbox/issues/339)) ([4db4527](https://github.com/usestring/gate-inbox/commit/4db4527715f3c2052805a42a7497ccc41a005547))
* **ui:** merge the focused scroll hint on macOS spellings ([#324](https://github.com/usestring/gate-inbox/issues/324)) ([2636b36](https://github.com/usestring/gate-inbox/commit/2636b36b6b9bb47d1561f15bd855b031a585a6fc))
* **ui:** place every muted triage row after unmuted rows ([#355](https://github.com/usestring/gate-inbox/issues/355)) ([7ce4c46](https://github.com/usestring/gate-inbox/commit/7ce4c461e41fb84e94afce16c87d5fe68793f975))
* **ui:** point an adopted pane at the CLI to reply ([#241](https://github.com/usestring/gate-inbox/issues/241)) ([7e98d65](https://github.com/usestring/gate-inbox/commit/7e98d6573f67af3b70559b97b7f75f64048e14e4))
* **ui:** put the focused hotkey menu's keys on the red band ([#387](https://github.com/usestring/gate-inbox/issues/387)) ([777e878](https://github.com/usestring/gate-inbox/commit/777e878aa629f7cbeb6e5ce65440c7ecca5ab600))
* **ui:** read every key hint from the live key map ([#280](https://github.com/usestring/gate-inbox/issues/280)) ([a4bbf9a](https://github.com/usestring/gate-inbox/commit/a4bbf9a18bc9abcbc6a029a4d2d6e002ff6d030c))


### Performance Improvements

* **config:** cache the decoded config.toml instead of re-parsing it per load ([#316](https://github.com/usestring/gate-inbox/issues/316)) ([ce96f59](https://github.com/usestring/gate-inbox/commit/ce96f5974c5c386884c6bca0e51b5dc9c089bed0))
* **deps:** build the rune-width table lazily by bumping go-runewidth to v0.0.31 ([#302](https://github.com/usestring/gate-inbox/issues/302)) ([d8e4300](https://github.com/usestring/gate-inbox/commit/d8e4300c888f23e26ce0cfb1ecb7ff0466fd6ff8))
* **sessioncmd:** answer a board read's liveness and directory in one tmux call ([#305](https://github.com/usestring/gate-inbox/issues/305)) ([99c9f3f](https://github.com/usestring/gate-inbox/commit/99c9f3f4e55fd25dc081b785c7815fa0b548afd4))
* **sessioncmd:** read every listed session's directory from one tmux call ([#301](https://github.com/usestring/gate-inbox/issues/301)) ([69b8144](https://github.com/usestring/gate-inbox/commit/69b8144d5477008bb3dc94b3c932e9464688bdeb))
* **store:** run the migrations once per process, not on every open ([#314](https://github.com/usestring/gate-inbox/issues/314)) ([530dbc5](https://github.com/usestring/gate-inbox/commit/530dbc5b2dca840142965f8b2499a9556216ff5d))
* **sysstat:** read the focused pane's process tree from /proc, not ps ([#306](https://github.com/usestring/gate-inbox/issues/306)) ([cc0ae63](https://github.com/usestring/gate-inbox/commit/cc0ae63fae590bd827d9cd01c4d968535e0af71f))
* **triage:** capture the handed-over pane at once, without the cursor's settle ([#372](https://github.com/usestring/gate-inbox/issues/372)) ([d859351](https://github.com/usestring/gate-inbox/commit/d8593514163ed8ae1fcf44ddaa605aeff2623ef3))
* **ui:** sample unfocused panes less often ([#319](https://github.com/usestring/gate-inbox/issues/319)) ([bc78f38](https://github.com/usestring/gate-inbox/commit/bc78f38bfcb01b4032d74f70ee75e7c86b7b1773))


### Reverts

* keep a skipped session off the drain (reverts [#397](https://github.com/usestring/gate-inbox/issues/397)) ([#402](https://github.com/usestring/gate-inbox/issues/402)) ([2febef6](https://github.com/usestring/gate-inbox/commit/2febef60100f42b8014e532de0e93440a81c483a))

## [0.12.0](https://github.com/usestring/gate-inbox/compare/v0.11.0...v0.12.0) (2026-10-03)


### Features

* let a parent grant its child a scoped, temporary permission ([#181](https://github.com/usestring/gate-inbox/issues/181)) ([f17931e](https://github.com/usestring/gate-inbox/commit/f17931ed1aac028b3ef37853c4b9ac3cbe966a8f))
* open the key map with its search on ([#230](https://github.com/usestring/gate-inbox/issues/230)) ([f2fa330](https://github.com/usestring/gate-inbox/commit/f2fa330e8efc8ee9858b280f2c91f1d000a84677))


### Bug Fixes

* **ui:** keep a turn raised during a triage visit ([#232](https://github.com/usestring/gate-inbox/issues/232)) ([2b34b0e](https://github.com/usestring/gate-inbox/commit/2b34b0eea760e39c4e307b606f163975342c7023))

## [0.11.0](https://github.com/usestring/gate-inbox/compare/v0.10.1...v0.11.0) (2026-10-03)


### Features

* keep newest turn full and expand shortened turns on hover ([#220](https://github.com/usestring/gate-inbox/issues/220)) ([489e83a](https://github.com/usestring/gate-inbox/commit/489e83a1119de61016ecf2f8182d91979a607378))
* read, relay, answer and preview child questions through each CLI's own record (S-145245) ([#171](https://github.com/usestring/gate-inbox/issues/171)) ([293baea](https://github.com/usestring/gate-inbox/commit/293baea21bb6dee4e97bce322b8df06ecea65437))
* read, relay, answer and verify OpenCode questions from the parent (S-145234) ([#173](https://github.com/usestring/gate-inbox/issues/173)) ([16fe36f](https://github.com/usestring/gate-inbox/commit/16fe36f6d3fcbdcc9455b1612894fe3eed3d60cd))
* **steering:** cut Gate Inbox's injected instructions to plain sentences ([#212](https://github.com/usestring/gate-inbox/issues/212)) ([05a129f](https://github.com/usestring/gate-inbox/commit/05a129ff38601dc7ec7e90d5e0762507d4c463a9))
* **ui:** fit more sessions on a phone-sized board ([#209](https://github.com/usestring/gate-inbox/issues/209)) ([a4dd15f](https://github.com/usestring/gate-inbox/commit/a4dd15fb35639c3ae3e694b7771b433eafa01542))
* **ui:** rank New Session prompt suggestions with JEV ([#227](https://github.com/usestring/gate-inbox/issues/227)) ([d09b9c9](https://github.com/usestring/gate-inbox/commit/d09b9c981fb1f9d86de402d7a3938a9f7ccab13f))
* **ui:** show session context tokens beside resource gauges ([#195](https://github.com/usestring/gate-inbox/issues/195)) ([b337e30](https://github.com/usestring/gate-inbox/commit/b337e30199bb1aca5dd1ae806e488230d7520983))


### Bug Fixes

* Automatically name untitled adopted panes ([#223](https://github.com/usestring/gate-inbox/issues/223)) ([8fe8c6a](https://github.com/usestring/gate-inbox/commit/8fe8c6ad996b71f8f9217fb628572a9e5122d055))
* keep children with background work running out of the auto-archive ([#218](https://github.com/usestring/gate-inbox/issues/218)) ([8884752](https://github.com/usestring/gate-inbox/commit/8884752b4b284032cc727df1b888362310fc6a2e))
* **mcp:** revive and migrate on the installed manager when the server is stale ([#215](https://github.com/usestring/gate-inbox/issues/215)) ([844971c](https://github.com/usestring/gate-inbox/commit/844971c18259a25e0ba1839e50159acc1a39cf03))
* open a Codex child in the directory it asked for ([#188](https://github.com/usestring/gate-inbox/issues/188)) ([72ef26f](https://github.com/usestring/gate-inbox/commit/72ef26fd97d0c6cb22ace117911b734ec1f92a33))
* **snippets:** trim the default shortcut set ([#219](https://github.com/usestring/gate-inbox/issues/219)) ([b709395](https://github.com/usestring/gate-inbox/commit/b7093957a3b6c8b6dfbc67b5498373f6e83c89ea))
* **ui:** keep live input visible in compressed focus [stack 3/3] ([#207](https://github.com/usestring/gate-inbox/issues/207)) ([2e4a5db](https://github.com/usestring/gate-inbox/commit/2e4a5db0dd7db7ec4b02342874ebe5434937d2e2))
* **ui:** keep the mobile layout on one panel when a phone zooms out ([#211](https://github.com/usestring/gate-inbox/issues/211)) ([5ebea04](https://github.com/usestring/gate-inbox/commit/5ebea0495037c525bb67ef46d123ad92be53534a))
* **ui:** keep the working spinner visible while reading older messages ([#225](https://github.com/usestring/gate-inbox/issues/225)) ([3c1ed63](https://github.com/usestring/gate-inbox/commit/3c1ed63758fa23e723f31c6ee54180d150cbe8f1))
* **ui:** make compressed focus an explicit experiment [stack 2/3] ([#206](https://github.com/usestring/gate-inbox/issues/206)) ([42c1273](https://github.com/usestring/gate-inbox/commit/42c12737512fc8566524bb3394466a6fef254044))
* **ui:** mark a child whose CLI exited and tell its spawner ([#189](https://github.com/usestring/gate-inbox/issues/189)) ([95b6887](https://github.com/usestring/gate-inbox/commit/95b6887cbad6ed292d91d392e7b64e6c4a336b66))

## [0.10.1](https://github.com/usestring/gate-inbox/compare/v0.10.0...v0.10.1) (2026-10-02)


### Bug Fixes

* **dialog:** read a question off the screen when the pending call is another dialog's ([#201](https://github.com/usestring/gate-inbox/issues/201)) ([7be1a47](https://github.com/usestring/gate-inbox/commit/7be1a471d0dd59507aab23e25572d577b355fbd2))
* **ui:** show the downstream TUI on focus by default ([#203](https://github.com/usestring/gate-inbox/issues/203)) ([13809b0](https://github.com/usestring/gate-inbox/commit/13809b0c91e74b772ff4e831b68dc775e9794270))

## [0.10.0](https://github.com/usestring/gate-inbox/compare/v0.9.0...v0.10.0) (2026-10-01)


### Features

* **ui:** default a focused session to its conversation ([#191](https://github.com/usestring/gate-inbox/issues/191)) ([a7722cd](https://github.com/usestring/gate-inbox/commit/a7722cddcda71362edc2a60fea5e8aa74b0bb5a1))


### Bug Fixes

* **dialog:** relay a permission prompt's command as it was written ([#197](https://github.com/usestring/gate-inbox/issues/197)) ([1041b6a](https://github.com/usestring/gate-inbox/commit/1041b6a5620192c8c7162765710ca416a2145a22))
* **hooks:** name each build's generated launch files after their content ([#192](https://github.com/usestring/gate-inbox/issues/192)) ([869cf65](https://github.com/usestring/gate-inbox/commit/869cf655e49db016ed5626fb031f9e9d4f844b32))
* **ui:** advance the drain when a hookless pane takes a submission ([#196](https://github.com/usestring/gate-inbox/issues/196)) ([256a052](https://github.com/usestring/gate-inbox/commit/256a0527cbd57e985c88b7a4e6eaa75a001bbbc1))

## [0.9.0](https://github.com/usestring/gate-inbox/compare/v0.8.0...v0.9.0) (2026-10-01)


### Features

* answer every child dialog kind through answer_session (S-145287) ([8651cc6](https://github.com/usestring/gate-inbox/commit/8651cc6c5fbc4e1b51d52d09951ab30714514861))
* clarify priority levels with directional triangles ([#182](https://github.com/usestring/gate-inbox/issues/182)) ([45b6e68](https://github.com/usestring/gate-inbox/commit/45b6e6861a1c237570422db09e98f63faefb06b6))
* **ui:** chain scroll keys into list navigation at pane edges ([b32a9f1](https://github.com/usestring/gate-inbox/commit/b32a9f1f80d7f8ac2fc788122a906d880e74cf5b))
* **ui:** suggest recurring prompts in New Session ([#183](https://github.com/usestring/gate-inbox/issues/183)) ([dd5c265](https://github.com/usestring/gate-inbox/commit/dd5c26523dbe6c21e6fb30a746671b62f3c39aae))
* verify a child's parent and attest relayed approvals (S-145286) ([#174](https://github.com/usestring/gate-inbox/issues/174)) ([1bc1571](https://github.com/usestring/gate-inbox/commit/1bc157133beaec2157265d080b68930d9001af15))


### Bug Fixes

* label / search as fuzzy session search, caret before placeholder ([#185](https://github.com/usestring/gate-inbox/issues/185)) ([cb50659](https://github.com/usestring/gate-inbox/commit/cb50659863fab8c2ac7436586c13953f152d3e18))
* **sessioncmd:** name the option a free-text answer landed on in a multi-question result (S-145331) ([#176](https://github.com/usestring/gate-inbox/issues/176)) ([38146a3](https://github.com/usestring/gate-inbox/commit/38146a36bfdac6e33d915e82a2b22e7111b72870))

## [0.8.0](https://github.com/usestring/gate-inbox/compare/v0.7.0...v0.8.0) (2026-10-01)


### Features

* add experimental JEV suggestions for existing sessions ([#161](https://github.com/usestring/gate-inbox/issues/161)) ([b282645](https://github.com/usestring/gate-inbox/commit/b282645fae55772a59222422c78077f246172d40))
* **ui:** separate the legend from the key map ([#175](https://github.com/usestring/gate-inbox/issues/175)) ([1c5cb4f](https://github.com/usestring/gate-inbox/commit/1c5cb4f12a238ad57e0b48484833672d51d11086))
* **ui:** show snippets in the list footer ([#169](https://github.com/usestring/gate-inbox/issues/169)) ([643db20](https://github.com/usestring/gate-inbox/commit/643db20ac248e8f14d2f4c33cbf1e1663ef96a02))


### Bug Fixes

* set an abandoned draft aside so queued messages reach the session ([#167](https://github.com/usestring/gate-inbox/issues/167)) ([3f3a987](https://github.com/usestring/gate-inbox/commit/3f3a98745ac84d452b06bb57975150b4e4770438))
* **ui:** make Left leave focus in triage instead of advancing ([#166](https://github.com/usestring/gate-inbox/issues/166)) ([b76dc6d](https://github.com/usestring/gate-inbox/commit/b76dc6d7780a2da6da023db62de3d9471a2c576d))

## [0.7.0](https://github.com/usestring/gate-inbox/compare/v0.6.0...v0.7.0) (2026-09-30)


### Features

* **ui:** group conversation messages by speaker [split 2/2] ([#163](https://github.com/usestring/gate-inbox/issues/163)) ([c7857cf](https://github.com/usestring/gate-inbox/commit/c7857cf24c1d14bc67ade18cafb9a0a444e903ca))
* **ui:** order a session's pull requests by what needs a person ([#160](https://github.com/usestring/gate-inbox/issues/160)) ([dbef576](https://github.com/usestring/gate-inbox/commit/dbef576273216d255147f74c6a3992c5df0fcb80))


### Bug Fixes

* display Codex opening prompts in the session rail ([#162](https://github.com/usestring/gate-inbox/issues/162)) ([34c807f](https://github.com/usestring/gate-inbox/commit/34c807f09ce3bda64626b961f54625ca5c9d5b55))
* keep sessions working while background work remains [split 1/2] ([#164](https://github.com/usestring/gate-inbox/issues/164)) ([2cd797f](https://github.com/usestring/gate-inbox/commit/2cd797f705a0bab791428eae659d0731c250850d))
* Require dialog-specific evidence before triage handover ([#156](https://github.com/usestring/gate-inbox/issues/156)) ([d513397](https://github.com/usestring/gate-inbox/commit/d513397da2b670adb16274fece570211943ebd2b))

## [0.6.0](https://github.com/usestring/gate-inbox/compare/v0.5.3...v0.6.0) (2026-09-30)


### Features

* relay every child dialog to its parent in full, and follow up while it stands (S-144508) ([#125](https://github.com/usestring/gate-inbox/issues/125)) ([ff1ae61](https://github.com/usestring/gate-inbox/commit/ff1ae614c985c1620cbb2ec169228835eb1ec104))
* **ui:** make space a hotkey-only snippet menu ([#118](https://github.com/usestring/gate-inbox/issues/118)) ([feb6158](https://github.com/usestring/gate-inbox/commit/feb6158c718b92d8a8ad035119d91d35d301fbdf))

## [0.5.3](https://github.com/usestring/gate-inbox/compare/v0.5.2...v0.5.3) (2026-09-30)


### Bug Fixes

* stop showing each session's opening prompt beneath its list row ([#154](https://github.com/usestring/gate-inbox/issues/154)) ([db6cbe4](https://github.com/usestring/gate-inbox/commit/db6cbe47e79294a7604cae10754bf741b5b25a01))
* **ui:** drop the rail's repeat of the cursor row's name and state ([#150](https://github.com/usestring/gate-inbox/issues/150)) ([ed04ab9](https://github.com/usestring/gate-inbox/commit/ed04ab9a92aa479413eb7c8d2c250d504a6c1464))
* Wait for tmux clients before taking over adopted panes ([#147](https://github.com/usestring/gate-inbox/issues/147)) ([7245c7b](https://github.com/usestring/gate-inbox/commit/7245c7b51800e5fdf2768634abc145ac81eb0d0d))

## [0.5.2](https://github.com/usestring/gate-inbox/compare/v0.5.1...v0.5.2) (2026-09-30)


### Bug Fixes

* write the OpenCode v2 config schema and carry its steering on the MCP server ([#146](https://github.com/usestring/gate-inbox/issues/146)) ([082bd73](https://github.com/usestring/gate-inbox/commit/082bd7369b4bd8bb0495150eb72f2ff6197e3f6e))

## [0.5.1](https://github.com/usestring/gate-inbox/compare/v0.5.0...v0.5.1) (2026-09-30)


### Bug Fixes

* delete an empty group on x instead of archiving it ([#140](https://github.com/usestring/gate-inbox/issues/140)) ([3a06325](https://github.com/usestring/gate-inbox/commit/3a0632559073e6ffd22e0c42b868bdb3fc22b705))
* stop relaying a detached session's lifecycle to its creator (S-144688) ([#123](https://github.com/usestring/gate-inbox/issues/123)) ([894762c](https://github.com/usestring/gate-inbox/commit/894762c571c6a1882c393ef1db2a6d9ae888341d))

## [0.5.0](https://github.com/usestring/gate-inbox/compare/v0.4.0...v0.5.0) (2026-09-29)


### Features

* **ui:** add a quick actions palette that teaches the keys ([#141](https://github.com/usestring/gate-inbox/issues/141)) ([cc2e188](https://github.com/usestring/gate-inbox/commit/cc2e18828bd2fa59ea46f332e2223e158109db35))

## [0.4.0](https://github.com/usestring/gate-inbox/compare/v0.3.0...v0.4.0) (2026-09-29)


### Features

* tell a session when its process tree hogs CPU or memory ([#120](https://github.com/usestring/gate-inbox/issues/120)) ([08b13fe](https://github.com/usestring/gate-inbox/commit/08b13fe81116a373c42997caa9ab7c178bd1056d))

## [0.3.0](https://github.com/usestring/gate-inbox/compare/v0.2.0...v0.3.0) (2026-09-29)


### Features

* open the key map with ctrl+h from the list, focus, and welcome ([#134](https://github.com/usestring/gate-inbox/issues/134)) ([2067713](https://github.com/usestring/gate-inbox/commit/2067713b775f0ad74b75d09b6cb9a1ec4f35a6a5))


### Miscellaneous Chores

* release 0.3.0 ([#137](https://github.com/usestring/gate-inbox/issues/137)) ([5094863](https://github.com/usestring/gate-inbox/commit/5094863f9ba0a2fefeae86e7637bb921348ea392))

## [0.2.0](https://github.com/usestring/gate-inbox/compare/v0.1.9...v0.2.0) (2026-09-29)


### Features

* add quota-paced Auto routing for new sessions ([#107](https://github.com/usestring/gate-inbox/issues/107)) ([f900dbb](https://github.com/usestring/gate-inbox/commit/f900dbbe6cd22bd4c0a153b2776ec2f0a789ef6d))
* nest a child agent's terminal under that child on the board (S-144474) ([#112](https://github.com/usestring/gate-inbox/issues/112)) ([29a7ba4](https://github.com/usestring/gate-inbox/commit/29a7ba43131444aac04fba0d2f556878f560636a))
* steer every managed CLI to Gate Inbox sessions over its own subagents ([#108](https://github.com/usestring/gate-inbox/issues/108)) ([392d9fe](https://github.com/usestring/gate-inbox/commit/392d9fec9b9df32735b42e6b7afb72dcd028ed8a))


### Bug Fixes

* give terminals the same cleanup as child sessions (S-144494) ([#115](https://github.com/usestring/gate-inbox/issues/115)) ([d9ecafe](https://github.com/usestring/gate-inbox/commit/d9ecafecee12a4c1dd895cd9da44dbbc0a0119f0))
* keep the triage drain off a session whose answer is still landing (S-144458) ([#110](https://github.com/usestring/gate-inbox/issues/110)) ([6b121e3](https://github.com/usestring/gate-inbox/commit/6b121e38a6de380428f147374e73f88bd575947b))

## [0.1.9](https://github.com/usestring/gate-inbox/compare/v0.1.8...v0.1.9) (2026-09-28)


### Bug Fixes

* release 0.1.9 through release-please ([#113](https://github.com/usestring/gate-inbox/issues/113)) ([7b0a153](https://github.com/usestring/gate-inbox/commit/7b0a153d4696e9ecc0f278096d197731076dc276))
