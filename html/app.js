
var ws = new WebSocket("ws://localhost:3000/websocket");

$(function(){

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			var url = $("#url").val();
			var val = {Key: 'url', Value: url};
			ws.send(JSON.stringify(val));
			$("#status").append(url + "\n");
		} else {
			$("#status").append("socket not ready\n")
		}
	});

	ws.onopen = function() {
	};

	ws.onmessage = function (e)	{
		var msg = JSON.parse(e.data);
		console.log(msg);
		if( 'Key' in msg ) {
			if( msg.Key == 'error') {
				$("#status").append("Error: " + msg.Value + "\n");
			}
			if( msg.Key == 'progress') {
				var pct = parseFloat(msg.Value.Pct);
				$("#progress-bar > span").css("width", pct + "%");
				$("#eta").text( msg.Value.ETA );
			}
			if( msg.Key == 'info') {
				$("#title").text( msg.Value.Title );
			}
		}
	};

	ws.onclose = function()	{
			$("#status").append("Connection closed\n");
	};

});
