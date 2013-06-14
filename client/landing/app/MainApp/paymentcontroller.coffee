class PaymentController extends KDController

  sanitizeRecurlyErrors = (fields, inputs, err) ->
    ERRORS =
      address1             :
        input              : inputs.address1
        field              : fields.address1
      address2             :
        input              : inputs.address2
        field              : fields.address2
      city                 :
        input              : inputs.city
        field              : fields.city
      state                :
        input              : inputs.state
        field              : fields.state
      country              :
        input              : inputs.country
        field              : fields.country
      first_name           :
        input              : inputs.cardFirstName
        field              : fields.cardFirstName
      last_name            :
        input              : inputs.cardLastName
        field              : fields.cardLastName
      number               :
        input              : inputs.cardNumber
        field              : fields.cardNumber
      zip                  :
        input              : inputs.zip
        field              : fields.zip
      verification_value   :
        input              : inputs.cardCV
        field              : fields.cardCV

    for key, val of ERRORS
      val.input.giveValidationFeedback no

    for e in err
      if e.field == 'account.base'
        val.input.showValidationError e.message
        if e.message.indexOf('card') > -1
          inputs.cardNumber.giveValidationFeedback yes
      else
        for key, val of ERRORS
          if e.field.indexOf(key) > -1
            val.input.giveValidationFeedback yes
            inputName = if val.input.inputLabel
              val.input.inputLabel.getTitle()
            else
              inputEl = val.input.$()[0]
              inputEl.getAttribute("placeholder") or inputEl.getAttribute("name") or ""

            val.input.showValidationError "#{inputName} #{e.message}"

  required = (msg)->
    rules    : required  : yes
    messages : required  : msg

  addPaymentMethod:(form, callback) ->
    formData = form.getFormData()

    delete formData.cardNumber  if formData.cardNumber.indexOf('...') > -1
    delete formData.cardCV      if formData.cardCV == 'XXX'


    KD.remote.api.JPayment.setAccount formData, (err, res) =>
      if err
        sanitizeRecurlyErrors form.fields, form.inputs, err
        callback? yes
      else
        sanitizeRecurlyErrors form.fields, form.inputs, []
        KD.remote.api.JPayment.getAccount {}, (e, r) =>
          unless e
            for k, v of r when form.inputs[k]
              form.inputs[k].setValue v
        callback? no

  validatePaymentMethodForm:(formData, callback)->

    return unless @modal

    form    = @modal.modalTabs.forms["Billing Info"]
    button  = @modal.buttons.Save
    onError = (err)->
      warn err
      sanitizeRecurlyErrors form.fields, form.inputs, err
      button.hideLoader()
    onSuccess = @modal.destroy.bind @modal
    callback formData, onError, onSuccess

  createPaymentMethodModal:(data, callback) ->

    @modal = modal = new KDModalViewWithForms
      title                       : "Billing Information"
      width                       : 520
      height                      : "auto"
      cssClass                    : "payments-modal"
      overlay                     : yes
      buttons                     :
        Save                      :
          title                   : "Save"
          style                   : "modal-clean-green"
          type                    : "button"
          loader                  : { color : "#ffffff", diameter : 12 }
          callback                : -> modal.modalTabs.forms["Billing Info"].submit()
      tabs                        :
        navigable                 : yes
        goToNextFormOnSubmit      : no
        forms                     :
          "Billing Info"          :
            callback              : (formData)=>
              @validatePaymentMethodForm formData, callback
            fields                :
              "intro"             :
                itemClass         : KDCustomHTMLView
                partial           : "<p>You can use pre-filled credit card information below to buy VM's <b>during beta</b>.</p>"
              cardFirstName       :
                label             : "Name"
                name              : "cardFirstName"
                placeholder       : "First Name"
                defaultValue      : KD.whoami().profile.firstName
                validate          : required "First name is required!"
                nextElementFlat   :
                  cardLastName    :
                    name          : "cardLastName"
                    placeholder   : "Last Name"
                    defaultValue  : KD.whoami().profile.lastName
                    validate      : required "Last name is required!"
              cardNumber          :
                label             : "Card Number"
                name              : "cardNumber"
                placeholder       : 'Card Number'
                defaultValue      : '4111-1111-1111-1111'
                validate          :
                  event           : "blur"
                  rules           :
                    creditCard    : yes
                nextElementFlat   :
                  cardCV          :
                    # tooltip       :
                    #   placement   : 'right'
                    #   direction   : 'center'
                    #   title       : 'The location of this verification number depends on the issuer of your credit card'
                    name          : "cardCV"
                    placeholder   : "CV Number"
                    defaultValue  : "123"
                    validate      :
                      rules       :
                        required  : yes
                        minLength : 3
                        regExp    : /[0-9]/
                      messages    :
                        required  : "Card security code is required! (CVV)"
                        minLength : "Card security code needs to be at least 3 digits!"
                        regExp    : "Card security code should be a number!"
              cardMonth           :
                label             : "Expire Date"
                itemClass         : KDSelectBox
                name              : "cardMonth"
                selectOptions     : __utils.getMonthOptions()
                defaultValue      : (new Date().getMonth())+2
                nextElementFlat   :
                  cardYear        :
                    itemClass     : KDSelectBox
                    name          : "cardYear"
                    selectOptions : __utils.getYearOptions((new Date().getFullYear()),(new Date().getFullYear()+25))
                    defaultValue  : (new Date().getFullYear())
              address1            :
                label             : "Address"
                name              : "address1"
                placeholder       : "Street Name & Number"
                defaultValue      : "358 Brannan Street"
                validate          : required "First address field is required!"
              address2            :
                label             : " "
                name              : "address2"
                placeholder       : "Apartment/Suite Number"
              city                :
                label             : "City & State"
                name              : "city"
                placeholder       : "City Name"
                defaultValue      : "San Francisco"
                validate          : required "City is required!"
                nextElementFlat   :
                  state           :
                    name          : "state"
                    placeholder   : "State"
                    defaultValue  : "CA"
                    validate      : required "State is required!"
              zip                 :
                label             : "ZIP & Country"
                name              : "zipCode"
                placeholder       : "ZIP Code"
                defaultValue      : "94107"
                validate          : required "Zip code is required!"
                nextElementFlat   :
                  country         :
                    name          : "country"
                    placeholder   : "Country"
                    defaultValue  : "United States of America"
                    validate      : required "First address field is required!"

    form = modal.modalTabs.forms["Billing Info"]

    form.on "FormValidationFailed", => modal.buttons.Save.hideLoader()

    for k, v of data
      if form.inputs[k]
        form.inputs[k].setValue v

    modal.on "KDObjectWillBeDestroyed", => delete @modal

    return modal

  deleteAccountPaymentMethod:(callback) ->

    @deleteModal = new KDModalView
      title        : "Warning"
      content      : "<div class='modalformline'>Are you sure you want to delete your billing information?</div>"
      height       : "auto"
      overlay      : yes
      buttons      :
        Yes        :
          loader   :
            color  : "#ffffff"
            diameter : 16
          style    : "modal-clean-gray"
          callback : ->
            KD.remote.api.JPayment.deleteAccountPaymentMethod {}, (err, res) ->
              modal.destroy()
              callback?()
