
var ws = new WebSocket("ws://localhost:3000/websocket");

$(function(){

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			var url = $("#url").val();
			var val = {key: 'url', value: url};
			ws.send(JSON.stringify(val));
			$("#status").append("\n" +  url);
		}
	});

	ws.onopen = function() {
	};

	ws.onmessage = function (evt)	{
			$("#status").append("\n" +   evt.data);
	};

	ws.onclose = function()	{
			$("#status").append("\n" +   "Connection closed");
	};

});
