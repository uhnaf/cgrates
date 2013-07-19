Apier.SetTPActions
++++++++++++++++++

Creates a new Actions profile within a tariff plan.

**Request**:

 Data:
  ::

   type TPActions struct {
	TPid      string   // Tariff plan id
	ActionsId string   // Actions id
	Actions   []Action // Set of actions this Actions profile will perform
   }

   type Action struct {
	Identifier     string  // Identifier mapped in the code
	BalanceId      string  // Type of balance the action will operate on
	Direction      string  // Balance direction
	Units          float64 // Number of units to add/deduct
	ExpirationTime int64   // Time when the units will expire
	DestinationId  string  // Destination profile id
	RateType       string  // Type of price <ABSOLUTE|PERCENT>
	Rate           float64 // Price value
	MinutesWeight  float64 // Minutes weight
	Weight         float64 // Action's weight
   }

 Mandatory parameters: ``[]string{"TPid", "ActionsId", "Actions", "Identifier", "Weight"}``

 *JSON sample*:
  ::

   {
    "id": 3, 
    "method": "Apier.SetTPActions", 
    "params": [
        {
            "Actions": [
                {
                    "BalanceId": "MONEY", 
                    "DestinationId": "CGRATES_NET", 
                    "Direction": "OUT", 
                    "ExpirationTime": 1374082259, 
                    "Identifier": "TOPUP_RESET", 
                    "MinutesWeight": 10, 
                    "Rate": 0.12, 
                    "RateType": "ABSOLUTE", 
                    "Units": 10, 
                    "Weight": 10
                }
            ], 
            "ActionsId": "SAMPLE_ACTS_1", 
            "TPid": "SAMPLE_TP_1"
        }
    ]
   }

**Reply**:

 Data:
  ::

   string

 Possible answers:
  ``OK`` - Success.

 *JSON sample*:
  ::

   {
    "error": null, 
    "id": 3, 
    "result": "OK"
   }

**Errors**:

 ``MANDATORY_IE_MISSING`` - Mandatory parameter missing from request.

 ``SERVER_ERROR`` - Server error occurred.

 ``DUPLICATE`` - The specified combination of TPid/ActionsId already present in StorDb.


Apier.GetTPActions
++++++++++++++++++

Queries specific Actions profile on tariff plan.

**Request**:

 Data:
  ::

   type AttrGetTPActions struct {
	TPid      string // Tariff plan id
	ActionsId string // Actions id
   }

 Mandatory parameters: ``[]string{"TPid", "ActionsId"}``

 *JSON sample*:
  ::

   {
    "id": 5, 
    "method": "Apier.GetTPActions", 
    "params": [
        {
            "ActionsId": "SAMPLE_ACTS_1", 
            "TPid": "SAMPLE_TP_1"
        }
    ]
   }
 
**Reply**:

 Data:
  ::

   type TPActions struct {
	TPid      string   // Tariff plan id
	ActionsId string   // Actions id
	Actions   []Action // Set of actions this Actions profile will perform
   }

   type Action struct {
	Identifier     string  // Identifier mapped in the code
	BalanceId      string  // Type of balance the action will operate on
	Direction      string  // Balance direction
	Units          float64 // Number of units to add/deduct
	ExpirationTime int64   // Time when the units will expire
	DestinationId  string  // Destination profile id
	RateType       string  // Type of price <ABSOLUTE|PERCENT>
	Rate           float64 // Price value
	MinutesWeight  float64 // Minutes weight
	Weight         float64 // Action's weight
   }

 *JSON sample*:
  ::

   {
    "error": null, 
    "id": 5, 
    "result": {
        "Actions": [
            {
                "BalanceId": "MONEY", 
                "DestinationId": "CGRATES_NET", 
                "Direction": "OUT", 
                "ExpirationTime": 1374082259, 
                "Identifier": "TOPUP_RESET", 
                "MinutesWeight": 10, 
                "Rate": 0.12, 
                "RateType": "ABSOLUTE", 
                "Units": 10, 
                "Weight": 10
            }
        ], 
        "ActionsId": "SAMPLE_ACTS_1", 
        "TPid": "SAMPLE_TP_1"
    }
   }

**Errors**:

 ``MANDATORY_IE_MISSING`` - Mandatory parameter missing from request.

 ``SERVER_ERROR`` - Server error occurred.

 ``NOT_FOUND`` - Requested Actions profile not found.


Apier.GetTPActionIds
++++++++++++++++++++

Queries Actions identities on specific tariff plan.

**Request**:

 Data:
  ::

   type AttrGetTPActionIds struct {
	TPid string // Tariff plan id
   }

 Mandatory parameters: ``[]string{"TPid"}``

 *JSON sample*:
  ::

   {
    "id": 6, 
    "method": "Apier.GetTPActionIds", 
    "params": [
        {
            "TPid": "SAMPLE_TP_1"
        }
    ]
   }

**Reply**:

 Data:
  ::

   []string

 *JSON sample*:
  ::

   {
    "error": null, 
    "id": 6, 
    "result": [
        "SAMPLE_ACTS_1", 
        "SAMPLE_ACTS_2"
    ]
   }

**Errors**:

 ``MANDATORY_IE_MISSING`` - Mandatory parameter missing from request.

 ``SERVER_ERROR`` - Server error occurred.

 ``NOT_FOUND`` - There are no Actions profiles defined on the selected TPid.

