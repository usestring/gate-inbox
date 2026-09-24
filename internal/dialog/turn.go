package dialog

// MaxTurnBytes bounds the worker text carried in an event. The manager reads
// the last turn to see what the worker was doing; a whole turn of tool output
// would bury that in a prompt somebody is paying for. textfmt.Tail makes the
// cut.
const MaxTurnBytes = 8 << 10
