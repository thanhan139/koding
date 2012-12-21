class AdminModal extends KDModalViewWithForms

  constructor : (options = {}, data) ->

    options =
      title                   : "Admin Panel"
      content                 : "<div class='modalformline'>With great power comes great responsibility. ~ Stan Lee</div>"
      overlay                 : yes
      width                   : 500
      height                  : "auto"
      cssClass                : "admin-kdmodal"
      tabs                    :
        navigable             : yes
        goToNextFormOnSubmit  : no
        forms                 :
          "User Details"      :
            buttons           :
              Update          :
                title         : "Update"
                style         : "modal-clean-gray"
                loader        :
                  color       : "#444444"
                  diameter    : 12
                callback      : =>
                  {inputs, buttons} = @modalTabs.forms["User Details"]
                  accounts = @userController.getSelectedItemData()
                  if accounts.length > 0
                    account  = accounts[0]
                    flags    = [flag.trim() for flag in inputs.Flags.getValue().split(",")][0]
                    account.updateFlags flags, (err)->
                      error err if err
                      KD.remote.api.JInvitation.grantInvitesFromClient
                        username : account.profile.nickname
                        quota    : +inputs.Invites.getValue()
                      , (err)=>
                        console.error err if err
                        new KDNotificationView {title: "Done!"}
                        buttons.Update.hideLoader()
                  else
                    new KDNotificationView {title : "Select a user first"}
            fields            :
              Username        :
                label         : "Dear User"
                type          : "hidden"
                nextElement   :
                  userWrapper :
                    itemClass : KDView
                    cssClass  : "completed-items"
              Flags           :
                label         : "Flags"
                placeholder   : "no flags assigned"
              Invites         :
                label         : "Grant Invites"
                type          : "text"
                placeholder   : "number of invites to add"
                validate      :
                  rules       :
                    regExp    : /\d+/i
                  messages    :
                    regExp    : "numbers only please"
              Impersonate     :
                label         : "Switch to User"
                itemClass     : KDButtonView
                title         : "Impersonate"
                callback      : =>
                  modal = new KDModalView
                    title          : "Switch to this user?"
                    content        : "<div class='modalformline'>This action will reload Koding and log you in with this user.</div>"
                    height         : "auto"
                    overlay        : yes
                    buttons        :
                      Impersonate  :
                        style      : "modal-clean-green"
                        loader     :
                          color    : "#FFF"
                          diameter : 16
                        callback   : =>
                          accounts = @userController.getSelectedItemData()
                          unless accounts.length is 0
                            KD.impersonate accounts[0].profile.nickname, =>
                              modal.destroy()
                          else
                            modal.destroy()
          "Send Beta Invites" :
            buttons           :
              "Send Invites"  :
                title         : "Send Invites"
                style         : "modal-clean-gray"
                loader        :
                  color       : "#444444"
                  diameter    : 12
                callback      : =>
                  {inputs, buttons} = @modalTabs.forms["Send Beta Invites"]
                  inputs.statusInfo.updatePartial 'Working on it...'
                  KD.remote.api.JInvitation.sendBetaInviteFromClient
                    batch     : +inputs.Count.getValue()
                  , (err, res)->
                    buttons['Send Invites'].hideLoader()
                    inputs.statusInfo.updatePartial res
                    console.log res, err
            fields            :
              Information     :
                label         : "Current state"
                type          : "hidden"
                nextElement   :
                  currentState:
                    itemClass : KDView
                    partial   : 'Loading...'
                    cssClass  : 'information-line'
              Count           :
                label         : "# of Invites"
                type          : "text"
                defaultValue  : 10
                placeholder   : "how many users do you want to Invite?"
                validate      :
                  rules       :
                    regExp    : /\d+/i
                  messages    :
                    regExp    : "numbers only please"
              Status          :
                label         : "Server response"
                type          : "hidden"
                nextElement   :
                  statusInfo  :
                    itemClass : KDView
                    partial   : '...'
                    cssClass  : 'information-line'

          "Migrate Kodingen Users" :
            buttons           :
              Migrate         :
                title         : "Migrate"
                style         : "modal-clean-gray"
                loader        :
                  color       : "#444444"
                  diameter    : 12
                callback      : =>
                  {inputs, buttons} = @modalTabs.forms["Migrate Kodingen Users"]
                  inputs.statusInfo.updatePartial 'Working on it...'
                  KD.remote.api.JOldUser.__migrateKodingenUsers
                    limit     : +inputs.Count.getValue()
                    delay     : +inputs.Delay.getValue()
                  , (err, res)->
                    buttons.Migrate.hideLoader()
                    inputs.statusInfo.updatePartial res
                    console.log res, err
              Stop            :
                title         : "Stop"
                style         : "modal-clean-red"
                loader        :
                  color       : "#444444"
                  diameter    : 12
                callback      : =>
                  form = @modalTabs.forms["Migrate Kodingen Users"]
                  form.inputs.statusInfo.updatePartial 'Trying to stop...'
                  KD.remote.api.JOldUser.__stopMigrate (err, res)->
                    form.buttons.Stop.hideLoader()
                    form.buttons.Migrate.hideLoader()
                    form.inputs.statusInfo.updatePartial res
                    console.log res, err
            fields            :
              Information     :
                label         : "Current state"
                type          : "hidden"
                nextElement   :
                  currentState:
                    itemClass : KDView
                    partial   : 'Loading...'
                    cssClass  : 'information-line'
              Count           :
                label         : "# of Migrate"
                type          : "text"
                defaultValue  : 10
                placeholder   : "how many users do you want to Migrate?"
                validate      :
                  rules       :
                    regExp    : /\d+/i
                  messages    :
                    regExp    : "numbers only please"
              Delay           :
                label         : "Delay (in sec.)"
                type          : "text"
                defaultValue  : 60
                placeholder   : "how many seconds do you need before create new user?"
                validate      :
                  rules       :
                    regExp    : /\d+/i
                  messages    :
                    regExp    : "numbers only please"
              Status          :
                label         : "Server response"
                type          : "hidden"
                nextElement   :
                  statusInfo  :
                    itemClass : KDView
                    partial   : '...'
                    cssClass  : 'information-line'

    super options, data

    @hideConnectedFields()

    @initInviteTab()
    @initMigrateTab()
    @createUserAutoComplete()

  createUserAutoComplete:->
    {fields, inputs, buttons} = @modalTabs.forms["User Details"]
    @userController = new KDAutoCompleteController
      form                : @modalTabs.forms["User Details"]
      name                : "userController"
      itemClass           : MemberAutoCompleteItemView
      itemDataPath        : "profile.nickname"
      outputWrapper       : fields.userWrapper
      selectedItemClass   : MemberAutoCompletedItemView
      listWrapperCssClass : "users"
      submitValuesAsText  : yes
      dataSource          : (args, callback)=>
        {inputValue} = args
        blacklist = (data.getId() for data in @userController.getSelectedItemData())
        KD.remote.api.JAccount.byRelevance inputValue, {blacklist}, (err, accounts)=>
          callback accounts

    @userController.on "ItemListChanged", =>
      accounts = @userController.getSelectedItemData()
      if accounts.length > 0
        account = accounts[0]
        inputs.Flags.setValue account.globalFlags?.join(',')
        userRequestLineEdit.hide()
        @showConnectedFields()
        account.fetchLimit? 'invite', (err, limit)=>
          current = 0
          if not err and limit
            current = limit.quota - limit.usage
          inputs.Invites.setPlaceHolder "Currently has #{current} invites."
      else
        userRequestLineEdit.show()
        @hideConnectedFields()

    fields.Username.addSubView userRequestLineEdit = @userController.getView()

  initInviteTab:->
    inviteForm = @modalTabs.forms["Send Beta Invites"]
    KD.remote.api.JInvitation.betaInviteCount (res)->
      inviteForm.inputs.currentState.updatePartial res

  initMigrateTab:->
    migrateForm = @modalTabs.forms["Migrate Kodingen Users"]
    KD.remote.api.JOldUser.__currentState (res)->
      migrateForm.inputs.currentState.updatePartial res

  hideConnectedFields:->
    {fields, inputs, buttons} = @modalTabs.forms["User Details"]
    fields.Impersonate.hide()
    buttons.Update.hide()
    fields.Invites.hide()
    inputs.Invites.setValue ''
    fields.Flags.hide()
    inputs.Flags.setValue ''

  showConnectedFields:->
    {fields, inputs, buttons} = @modalTabs.forms["User Details"]
    fields.Impersonate.show()
    fields.Flags.show()
    fields.Invites.show()
    inputs.Invites.setPlaceHolder 'Loading...'
    buttons.Update.show()
